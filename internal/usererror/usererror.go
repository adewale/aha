// Package usererror is the only translation layer from causal/internal errors
// to stable, credential-safe user-facing failures.
package usererror

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/aws/smithy-go"
)

type Code string

const (
	CodeUnknownCommand         Code = "unknown_command"
	CodeInvalidInput           Code = "validation_error"
	CodeFlagParse              Code = "flag_parse_error"
	CodeNotFound               Code = "not_found"
	CodeAmbiguous              Code = "ambiguous"
	CodeUnsupported            Code = "unsupported"
	CodeUnsupportedSchema      Code = "unsupported_schema"
	CodeUnsupportedRef         Code = "unsupported_ref"
	CodePermissionDenied       Code = "permission_denied"
	CodePrivacyAcknowledgement Code = "privacy_acknowledgement_required"
	CodeUnavailable            Code = "unavailable"
	CodeConflict               Code = "conflict"
	CodeCorruptData            Code = "corrupt_data"
	CodeCancelled              Code = "cancelled"
	CodeCommandFailed          Code = "command_failed"
)

type Action struct {
	command string
	args    []string
}

func (a Action) Command() string { return a.command }
func (a Action) Args() []string  { return append([]string(nil), a.args...) }

func (a Action) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{Command: a.command, Args: a.Args()})
}

func (a Action) Text() string {
	parts := append([]string{a.command}, a.args...)
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

type View struct {
	code           Code
	message        string
	command        string
	next           Action
	diagnosticKind string
	retryable      bool
}

func (v View) Code() Code      { return v.code }
func (v View) Message() string { return v.message }
func (v View) Command() string { return v.command }
func (v View) Next() Action    { return v.next }

type Diagnostic struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	Retryable bool   `json:"retryable"`
}

// Diagnostics intentionally contains only allowlisted classifications. Raw
// error strings, paths, SQL, URLs, object keys, and credential values cannot
// enter this type.
func Diagnostics(view View) []Diagnostic {
	return []Diagnostic{{Kind: view.diagnosticKind, Operation: view.command, Retryable: view.retryable}}
}

type publicError struct {
	view  View
	cause error
}

func (e *publicError) Error() string { return e.view.message }
func (e *publicError) Unwrap() error { return e.cause }

func Wrap(view View, cause error) error { return &publicError{view: view, cause: cause} }

func UnknownCommand(command string, cause error) View {
	return view(CodeUnknownCommand, fmt.Sprintf("Unknown aha command %q.", command), command, helpAction(""), "usage", false)
}

func InvalidInput(command string, cause error) View {
	return view(CodeInvalidInput, commandMessage(command, "received invalid input"), command, helpAction(command), "validation", false)
}

func PrivacyAcknowledgement(command string) View {
	if command != "snapshot" && command != "refresh" {
		command = "snapshot"
	}
	return view(
		CodePrivacyAcknowledgement,
		"Raw snapshot privacy acknowledgement is required before upload.",
		command,
		action("aha", command, "--accept-secrets"),
		"privacy_acknowledgement",
		false,
	)
}

func Normalize(err error, command string) View {
	if err == nil {
		panic("usererror.Normalize requires a non-nil error")
	}
	if errors.Is(err, context.Canceled) {
		return view(CodeCancelled, commandMessage(command, "was cancelled before completion"), command, doctorAction(), "cancelled", true)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return view(CodeUnavailable, commandMessage(command, "timed out before completion"), command, doctorAction(), "timeout", true)
	}
	if errors.Is(err, flag.ErrHelp) {
		return view(CodeFlagParse, commandMessage(command, "needs valid command options"), command, helpAction(command), "usage", false)
	}
	var auth *depot.R2AuthorizationError
	if errors.As(err, &auth) {
		return view(CodePermissionDenied,
			fmt.Sprintf("R2 denied both bucket access checks for %q before any depot mutation.", auth.Bucket),
			command, action("aha", "depot", "setup", "r2:"+auth.Bucket, "--json"), "r2_authorization", false)
	}
	var contention *depot.ContentionError
	if errors.As(err, &contention) {
		return view(CodeConflict, "The depot stayed busy because other writers repeatedly changed shared state.", command, doctorAction(), "depot_contention", true)
	}
	var notFound corpus.NotFoundError
	if errors.As(err, &notFound) {
		return view(CodeNotFound, fmt.Sprintf("The requested %s was not found.", safeKind(notFound.Kind)), command, action("aha", "status", "--json"), "corpus_not_found", false)
	}
	var ambiguous corpus.AmbiguousError
	if errors.As(err, &ambiguous) {
		return view(CodeAmbiguous, fmt.Sprintf("The requested %s matches more than one result.", safeKind(ambiguous.Kind)), command, helpAction(command), "ambiguous_reference", false)
	}
	var unsupportedSchema archive.UnsupportedSchemaError
	if errors.As(err, &unsupportedSchema) {
		return view(CodeUnsupportedSchema, "The input uses an unsupported archive schema.", command, helpAction(command), "unsupported_schema", false)
	}
	var unsupportedRef model.UnsupportedRefError
	if errors.As(err, &unsupportedRef) {
		return view(CodeUnsupportedRef, "The reference format is not supported by this version of aha.", command, helpAction(command), "unsupported_reference", false)
	}
	if errors.Is(err, corpus.ErrRebuildUnsupported) {
		return view(CodeUnsupported, "Safe corpus rebuild is not supported on this platform.", command, helpAction("corpus"), "unsupported_platform", false)
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return view(CodePermissionDenied, commandMessage(command, "could not access required local storage because permission was denied"), command, doctorAction(), "filesystem_permission", false)
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
		return view(CodeNotFound, commandMessage(command, "could not find required local state"), command, doctorAction(), "filesystem_not_found", false)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(apiErr.ErrorCode()) {
		case "accessdenied", "forbidden", "403", "invalidaccesskeyid", "signaturedoesnotmatch":
			return view(CodePermissionDenied, "The remote depot rejected the configured credentials or endpoint.", command, doctorAction(), "remote_authorization", false)
		case "nosuchbucket", "nosuchkey", "notfound", "404":
			return view(CodeNotFound, "The requested remote depot resource was not found.", command, doctorAction(), "remote_not_found", false)
		case "preconditionfailed", "412", "conflict":
			return view(CodeConflict, "The remote depot changed concurrently; the operation could not safely finish.", command, doctorAction(), "remote_conflict", true)
		case "slowdown", "throttling", "throttlingexception", "503", "serviceunavailable":
			return view(CodeUnavailable, "The remote depot is temporarily unavailable or throttling requests.", command, doctorAction(), "remote_throttled", true)
		default:
			return view(CodeUnavailable, "The remote depot operation failed.", command, doctorAction(), "remote_failure", true)
		}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "placeholder") && strings.Contains(msg, "r2") {
		return view(CodeInvalidInput, "R2 configuration contains a documentation placeholder instead of a real value.", command, doctorAction(), "r2_placeholder", false)
	}
	if strings.Contains(msg, "invalid r2 bucket") {
		return view(CodeInvalidInput, "The R2 bucket name is invalid.", command, doctorAction(), "r2_bucket", false)
	}
	if strings.Contains(msg, "r2 credentials required") {
		return view(CodePermissionDenied, "R2 S3 credentials are missing or invalid.", command, doctorAction(), "r2_credentials", false)
	}
	if strings.Contains(msg, "r2 account id required") || strings.Contains(msg, "invalid r2 account id") {
		return view(CodeInvalidInput, "The R2 account ID is missing or invalid.", command, doctorAction(), "r2_account", false)
	}
	if strings.Contains(msg, "conflicting r2 environment aliases") {
		return view(CodeInvalidInput, "Conflicting R2 environment aliases are set to different values.", command, doctorAction(), "r2_alias_conflict", false)
	}
	if strings.Contains(msg, "flag provided but not defined") || strings.Contains(msg, "invalid value") || strings.Contains(msg, "flag needs an argument") || strings.Contains(msg, "requires path") || strings.Contains(msg, "unknown corpus subcommand") || strings.Contains(msg, "unknown depot subcommand") {
		return view(CodeFlagParse, commandMessage(command, "received invalid command options"), command, helpAction(command), "usage", false)
	}
	if strings.Contains(msg, "address already in use") {
		return view(CodeUnavailable, "The dashboard address is already in use.", command, helpAction("serve"), "address_in_use", true)
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "tls handshake") || strings.Contains(msg, "x509:") {
		return view(CodeUnavailable, "The remote service could not be reached securely.", command, doctorAction(), "network_unavailable", true)
	}
	if strings.Contains(msg, "no space left") {
		return view(CodeUnavailable, "Local storage is full.", command, doctorAction(), "storage_full", true)
	}
	if strings.Contains(msg, "read-only file system") {
		return view(CodePermissionDenied, "Required local storage is read-only.", command, doctorAction(), "filesystem_read_only", false)
	}
	if strings.Contains(msg, "broken pipe") {
		return view(CodeUnavailable, "The output destination closed before the command finished.", command, helpAction(command), "output_closed", true)
	}
	if strings.Contains(msg, "required") || strings.Contains(msg, "cannot be combined") || strings.Contains(msg, "must be") || strings.Contains(msg, "invalid ") {
		return view(CodeInvalidInput, commandMessage(command, "received invalid input"), command, helpAction(command), "validation", false)
	}
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") {
		return view(CodeUnavailable, "The corpus database is busy with another operation.", command, doctorAction(), "database_busy", true)
	}
	if strings.Contains(msg, "constraint failed") || strings.Contains(msg, "unique constraint") {
		return view(CodeConflict, "Stored state conflicts with an existing record.", command, doctorAction(), "database_conflict", false)
	}
	if strings.Contains(msg, "malformed") || strings.Contains(msg, "database disk image is malformed") || strings.Contains(msg, "corrupt") || strings.Contains(msg, "sha mismatch") || strings.Contains(msg, "checksum mismatch") {
		return view(CodeCorruptData, "Stored data failed an integrity check.", command, doctorAction(), "corrupt_data", false)
	}
	return view(CodeCommandFailed, commandMessage(command, "could not complete"), command, doctorAction(), "internal_failure", false)
}

func view(code Code, message, command string, next Action, diagnostic string, retryable bool) View {
	return View{code: code, message: message, command: command, next: next, diagnosticKind: diagnostic, retryable: retryable}
}

func action(command string, args ...string) Action {
	if command == "" {
		panic("usererror action requires a command")
	}
	return Action{command: command, args: append([]string(nil), args...)}
}

func HelpAction(command string) Action { return helpAction(command) }

func doctorAction() Action { return action("aha", "doctor", "--json") }

func helpAction(command string) Action {
	if command == "" {
		return action("aha", "help")
	}
	return action("aha", command, "--help")
}

func commandMessage(command, suffix string) string {
	if command == "" {
		return "The aha command " + suffix + "."
	}
	return fmt.Sprintf("The aha %s command %s.", command, suffix)
}

func safeKind(kind string) string {
	switch kind {
	case "session", "entry", "artifact", "snapshot", "machine", "reference":
		return kind
	default:
		return "item"
	}
}

func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\"'\\$`;&|<>()*?[]{}!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
