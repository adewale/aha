package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/usererror"
)

type errorOptions struct{ Verbose bool }

func errorOptionsFromArgs(args []string) ([]string, errorOptions) {
	clean := make([]string, 0, len(args))
	var opts errorOptions
	for i, arg := range args {
		if arg == "--" {
			clean = append(clean, args[i:]...)
			break
		}
		if arg == "--verbose-errors" {
			opts.Verbose = true
			continue
		}
		clean = append(clean, arg)
	}
	return clean, opts
}

func publicErrorView(err error, args []string) usererror.View {
	command := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.Code == "unknown_command" {
		return usererror.UnknownCommand(ce.Command, err)
	}
	return usererror.Normalize(err, command)
}

func renderHumanError(w io.Writer, view usererror.View, verbose bool) {
	fmt.Fprintf(w, "error: %s\n", view.Message())
	fmt.Fprintf(w, "next: %s\n", view.Next().Text())
	if verbose {
		for _, diagnostic := range usererror.Diagnostics(view) {
			fmt.Fprintf(w, "diagnostic: kind=%s operation=%s retryable=%v\n", diagnostic.Kind, diagnostic.Operation, diagnostic.Retryable)
		}
	}
}

func structuredProgressRequested(args []string) bool {
	for i, arg := range args {
		if arg == "--progress=json" || (arg == "--progress" && i+1 < len(args) && args[i+1] == "json") {
			return true
		}
	}
	return false
}

func writePresentedError(w io.Writer, envelope errorEnvelope, ndjson bool) error {
	if ndjson {
		return json.NewEncoder(w).Encode(envelope)
	}
	return writeJSON(w, envelope)
}

func flagOutput(args []string, stderr io.Writer) io.Writer {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return stderr
		}
	}
	return io.Discard
}
