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
	"syscall"
	"time"

	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/server"
)

// cmdServe runs the local dashboard. Read-only by design and bound to
// loopback unless --allow-remote is set. See docs/mcp-spec.md (phase 3).
func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	addr := fs.String("addr", "127.0.0.1:18428", "listen address (loopback only unless --allow-remote)")
	allowRemote := fs.Bool("allow-remote", false, "allow non-loopback bind (off by default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*allowRemote && os.Getenv("AHA_ALLOW_REMOTE") == "1" {
		*allowRemote = true
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
	srv := server.New(backend)
	listener, err := server.Listen(server.Options{Addr: *addr, AllowRemote: *allowRemote})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aha dashboard listening on http://%s\n", listener.Addr().String())

	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
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
