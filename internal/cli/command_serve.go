package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/server"
	"github.com/adewale/aha/internal/usererror"
)

// cmdDashboard runs the local dashboard. Read-only by design and bound to
// loopback unless --allow-remote is set. See docs/mcp-spec.md (phase 3).
func cmdDashboard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	cf := registerWorkspaceFlags(fs)
	addr := fs.String("addr", "127.0.0.1:18428", "listen address (loopback only unless --allow-remote)")
	allowRemote := fs.Bool("allow-remote", false, "allow non-loopback bind (off by default)")
	allowedHosts := fs.String("allowed-hosts", "", "comma-separated Host header values to accept in addition to loopback (use with --allow-remote)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request handler timeout")
	token := fs.String("token", "", "require this bearer token on every request (REQUIRED with --allow-remote; AHA_DASHBOARD_TOKEN env var also honored)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*allowRemote && os.Getenv("AHA_ALLOW_REMOTE") == "1" {
		*allowRemote = true
	}
	if *token == "" {
		*token = os.Getenv("AHA_DASHBOARD_TOKEN")
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()

	backend := mcp.NewCorpusBackend(store, cfg)
	opts := server.Options{
		Addr:         *addr,
		AllowRemote:  *allowRemote,
		AllowedHosts: splitCSV(*allowedHosts),
		Token:        *token,
	}
	srv := server.NewWithOptions(backend, opts)
	listener, err := server.Listen(opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aha dashboard listening on http://%s\n", listener.Addr().String())

	timeoutAction := usererror.HelpAction("serve")
	timeoutBody := mustJSON(map[string]any{"error": map[string]any{
		"code": "timeout", "message": "request timed out",
		"next": []string{timeoutAction.Text()}, "next_action": timeoutAction,
	}}) + "\n"
	handler := http.TimeoutHandler(srv, *timeout, timeoutBody)
	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
