package usererror_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/usererror"
)

func TestNormalizeAlwaysReturnsOneSafeActionAndPreservesCause(t *testing.T) {
	cause := errors.New("secret-canary raw internal failure")
	view := usererror.Normalize(cause, "search")
	if view.Code() != usererror.CodeCommandFailed || view.Message() == "" {
		t.Fatalf("view=%+v", view)
	}
	if view.Next().Command() == "" || len(view.Next().Args()) == 0 {
		t.Fatalf("missing sole next action: %+v", view.Next())
	}
	encoded := fmt.Sprintf("%+v", view)
	if strings.Contains(encoded, "secret-canary") {
		t.Fatalf("public view leaked cause: %s", encoded)
	}
}

func TestNormalizeHidesFilesystemPaths(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private", "corpus.db")
	err := &os.PathError{Op: "open", Path: privatePath, Err: os.ErrPermission}
	view := usererror.Normalize(err, "verify")
	if view.Code() != usererror.CodePermissionDenied || strings.Contains(view.Message(), privatePath) {
		t.Fatalf("view=%+v", view)
	}
}

func TestNormalizeStalePublicationAsRetryableConflict(t *testing.T) {
	view := usererror.Normalize(&depot.StalePublicationError{Machine: "machine"}, "snapshot")
	if view.Code() != usererror.CodeConflict {
		t.Fatalf("view=%+v want conflict", view)
	}
	diagnostics := usererror.Diagnostics(view)
	if len(diagnostics) != 1 || !diagnostics[0].Retryable {
		t.Fatalf("diagnostics=%+v want retryable", diagnostics)
	}
}

func TestNormalizeCancellationAndR2Authorization(t *testing.T) {
	cancelled := usererror.Normalize(context.Canceled, "refresh")
	if cancelled.Code() != usererror.CodeCancelled {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	cause := errors.New("sdk secret-canary")
	auth := &depot.R2AuthorizationError{Bucket: "safe-bucket", Cause: cause}
	view := usererror.Normalize(auth, "depot")
	if view.Code() != usererror.CodePermissionDenied || !strings.Contains(view.Message(), "safe-bucket") || strings.Contains(fmt.Sprintf("%+v", view), "secret-canary") {
		t.Fatalf("view=%+v", view)
	}
	if !errors.Is(usererror.Wrap(view, auth), cause) {
		t.Fatal("public wrapper lost causal errors.Is identity")
	}
}

func TestDiagnosticIsAllowlistedRatherThanRawCause(t *testing.T) {
	view := usererror.Normalize(errors.New("password=secret-canary /private/path SQL select *"), "status")
	diagnostics := usererror.Diagnostics(view)
	joined := fmt.Sprintf("%+v", diagnostics)
	if strings.Contains(joined, "secret-canary") || strings.Contains(joined, "/private/path") || strings.Contains(joined, "select *") {
		t.Fatalf("diagnostics leaked raw cause: %s", joined)
	}
	if len(diagnostics) == 0 {
		t.Fatal("verbose diagnostics should identify a safe failure class")
	}
}
