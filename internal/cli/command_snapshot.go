package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type snapshotRequest struct {
	Config          model.Config
	DepotOverride   string
	CapturedAt      string
	BundleID        string
	SessionFilters  []string
	MaxSessions     int
	JSON            bool
	SkipIfUnchanged bool
}

func cmdSnapshot(args []string, stdout, stderr io.Writer) error {
	req, err := parseSnapshotRequest("snapshot", args, stderr)
	if err != nil {
		return err
	}
	finalizeSnapshotMetadata(&req)
	path, sha, err := writeSnapshot(req)
	if err != nil {
		return err
	}
	if req.JSON {
		return writeJSON(stdout, map[string]any{"bundle": path, "sha256": sha, "bundle_id": req.BundleID, "captured_at": req.CapturedAt})
	}
	fmt.Fprintf(stdout, "%s\nsha256:%s\n", path, sha)
	return nil
}

func cmdRefresh(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "machine id")
	depotAddr := fs.String("depot", "", "depot address")
	corpusDir := fs.String("corpus", "", "corpus dir")
	repoDir := fs.String("repo", "", "repo/corpus dir")
	configPath := fs.String("config", "", "JSONC config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "acknowledge v1 does not redact secrets")
	capturedAt := fs.String("captured-at", "", "capture timestamp (advanced deterministic testing)")
	bundleID := fs.String("bundle-id", "", "bundle id (advanced deterministic testing)")
	maxSessions := fs.Int("max-sessions", 0, "maximum number of discovered local sessions to snapshot (0 means all)")
	jsonOut := fs.Bool("json", false, "JSON output")
	var sourceFlags multiFlag
	var sessionFlags multiFlag
	fs.Var(&sourceFlags, "source", "source spec type=path (repeatable)")
	fs.Var(&sessionFlags, "session", "session id/path match to snapshot (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, err := buildSnapshotRequest(*configPath, *machine, *depotAddr, *acceptSecrets, *capturedAt, *bundleID, sourceFlags, sessionFlags, *maxSessions, stderr)
	if err != nil {
		return err
	}
	req.SkipIfUnchanged = *capturedAt == "" && *bundleID == ""
	applyCorpusOverride(&req.Config, *repoDir, *corpusDir)
	if err := safety.ValidateWriteOutsideSources(req.Config, req.Config.CorpusDir, "corpus"); err != nil {
		return err
	}
	finalizeSnapshotMetadata(&req)
	path, sha, err := writeSnapshot(req)
	if err != nil {
		return err
	}
	if !*jsonOut {
		fmt.Fprintf(stdout, "%s\nsha256:%s\n", path, sha)
	}
	store, err := openCorpusForCommand(req.Config, true)
	if err != nil {
		return err
	}
	defer store.Close()
	drv, err := depotDriverForConfig(req.Config, req.DepotOverride)
	if err != nil {
		return err
	}
	reports, err := ingestFromDepot(stdout, store, drv, *jsonOut)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"bundle": path, "sha256": sha, "report": summarizeReports(reports), "reports": reports})
	}
	return nil
}

func parseSnapshotRequest(name string, args []string, stderr io.Writer) (snapshotRequest, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "machine id")
	depotAddr := fs.String("depot", "", "depot address")
	configPath := fs.String("config", "", "JSONC config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "acknowledge v1 does not redact secrets")
	capturedAt := fs.String("captured-at", "", "capture timestamp (advanced deterministic testing)")
	bundleID := fs.String("bundle-id", "", "bundle id (advanced deterministic testing)")
	maxSessions := fs.Int("max-sessions", 0, "maximum number of discovered local sessions to snapshot (0 means all)")
	jsonOut := fs.Bool("json", false, "JSON output")
	var sourceFlags multiFlag
	var sessionFlags multiFlag
	fs.Var(&sourceFlags, "source", "source spec type=path (repeatable)")
	fs.Var(&sessionFlags, "session", "session id/path match to snapshot (repeatable)")
	if err := fs.Parse(args); err != nil {
		return snapshotRequest{}, err
	}
	req, err := buildSnapshotRequest(*configPath, *machine, *depotAddr, *acceptSecrets, *capturedAt, *bundleID, sourceFlags, sessionFlags, *maxSessions, stderr)
	if err != nil {
		return snapshotRequest{}, err
	}
	req.JSON = *jsonOut
	return req, nil
}

func finalizeSnapshotMetadata(req *snapshotRequest) {
	if req.CapturedAt == "" {
		req.CapturedAt = ahaclock.RealClock{}.Now().Format(time.RFC3339)
	}
	if req.BundleID == "" {
		req.BundleID = hash.RandomID()
	}
}

func buildSnapshotRequest(configPath, machine, depotAddr string, acceptSecrets bool, capturedAt, bundleID string, sourceFlags, sessionFlags multiFlag, maxSessions int, stderr io.Writer) (snapshotRequest, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return snapshotRequest{}, err
	}
	if machine != "" {
		cfg.MachineID = machine
	}
	if len(sourceFlags) > 0 {
		cfg.Sources = nil
		for _, sf := range sourceFlags {
			parts := strings.SplitN(sf, "=", 2)
			if len(parts) != 2 {
				return snapshotRequest{}, fmt.Errorf("invalid --source %q, want type=path", sf)
			}
			cfg.Sources = append(cfg.Sources, model.SourceConfig{Type: parts[0], Root: parts[1], Enabled: true})
		}
	}
	if cfg.MachineID == "" {
		return snapshotRequest{}, errors.New("machine_id required: set config machine_id or pass --machine")
	}
	if os.Getenv("AHA_ACCEPT_SECRETS") == "1" {
		cfg.AcceptSecretsWarning = true
	}
	if !acceptSecrets && !cfg.AcceptSecretsWarning {
		fmt.Fprintln(stderr, "V1 does not redact secrets. Bundles may contain prompts, source code, tool output, images, tokens, and private paths. Treat the bundle as private. Pass --accept-secrets to continue.")
		return snapshotRequest{}, errors.New("secrets warning not acknowledged")
	}
	if maxSessions < 0 {
		return snapshotRequest{}, errors.New("--max-sessions must be >= 0")
	}
	depotAddress := depot.AddressFromConfig(cfg.Depot)
	if depotAddr != "" {
		parsed, err := depot.ParseAddress(depotAddr)
		if err != nil {
			return snapshotRequest{}, err
		}
		depotAddress = parsed
	}
	if depotAddress.Type == "local" {
		if err := safety.ValidateWriteOutsideSources(cfg, depotAddress.Location, "depot"); err != nil {
			return snapshotRequest{}, err
		}
	}
	return snapshotRequest{Config: cfg, DepotOverride: depotAddr, CapturedAt: capturedAt, BundleID: bundleID, SessionFilters: []string(sessionFlags), MaxSessions: maxSessions}, nil
}
