package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
	"github.com/adewale/aha/internal/safety"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

var errPrivacyAcknowledgement = errors.New("privacy acknowledgement required")

type snapshotRequest struct {
	Context         context.Context
	Config          model.Config
	DepotOverride   string
	CapturedAt      string
	SessionFilters  []string
	MaxSessions     int
	JSON            bool
	Progress        *ahaprogress.Tracker
	ProgressSetting string
	// Force bypasses the advisory capture cache (full re-read).
	Force bool
}

func cmdSnapshot(args []string, stdout, stderr io.Writer) error {
	return runSnapshotContext(context.Background(), args, stdout, stderr)
}

func runSnapshotContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	req, err := parseSnapshotRequest("snapshot", args, stderr)
	if err != nil {
		return err
	}
	progress, err := newProgressSession(stderr, req.ProgressSetting, req.JSON)
	if err != nil {
		return err
	}
	defer progress.Close()
	req.Context = ctx
	req.Progress = progress.Tracker
	res, err := pushSnapshotV2(req)
	if err != nil {
		return err
	}
	if req.JSON {
		return writeJSON(stdout, pushResultJSON(res))
	}
	printPushResult(stdout, res)
	return nil
}

func cmdRefresh(args []string, stdout, stderr io.Writer) error {
	return runRefreshContext(context.Background(), args, stdout, stderr)
}

func runRefreshContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	snapshotFlags := registerSnapshotFlags(fs)
	corpusDir := fs.String("corpus", "", "corpus dir")
	repoDir := fs.String("repo", "", "repo/corpus dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, err := snapshotFlags.buildRequest()
	if err != nil {
		return err
	}
	progress, err := newProgressSession(stderr, req.ProgressSetting, req.JSON)
	if err != nil {
		return err
	}
	defer progress.Close()
	req.Context = ctx
	req.Progress = progress.Tracker
	applyCorpusOverride(&req.Config, *repoDir, *corpusDir)
	destination, err := prepareWritableCorpus(req.Config)
	if err != nil {
		return err
	}
	res, err := pushSnapshotV2(req)
	if err != nil {
		return err
	}
	if !*snapshotFlags.jsonOut {
		printPushResult(stdout, res)
	}
	v2, err := depotV2ForConfig(req.Config, req.DepotOverride)
	if err != nil {
		return err
	}
	prepared, err := v2.PreparePull(ctx)
	if err != nil {
		return err
	}
	store, err := openPreparedCorpus(destination)
	if err != nil {
		return err
	}
	defer store.Close()
	ing, err := ingestorForConfig(store, req.Config)
	if err != nil {
		return err
	}
	ing.Context = ctx
	reports, err := pullFromDepotV2(ctx, stdout, ing, prepared, *snapshotFlags.jsonOut, req.Progress)
	if err != nil {
		return err
	}
	if *snapshotFlags.jsonOut {
		return writeJSON(stdout, map[string]any{"push": pushResultJSON(res), "report": summarizeReports(reports), "reports": reports})
	}
	return nil
}

func pushResultJSON(res depot.PushResult) map[string]any {
	return map[string]any{"manifest_sha256": res.ManifestSHA256().String(), "reused": res.Reused, "files": res.Files, "blobs_uploaded": res.BlobsUploaded, "blobs_existing": res.BlobsExisting, "blobs_carried": res.BlobsCarried}
}

func printPushResult(stdout io.Writer, res depot.PushResult) {
	fmt.Fprintf(stdout, "snapshot %s files=%d uploaded=%d existing=%d carried=%d reused=%v\n", res.ManifestSHA256(), res.Files, res.BlobsUploaded, res.BlobsExisting, res.BlobsCarried, res.Reused)
}

func parseSnapshotRequest(name string, args []string, stderr io.Writer) (snapshotRequest, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	snapshotFlags := registerSnapshotFlags(fs)
	if err := fs.Parse(args); err != nil {
		return snapshotRequest{}, err
	}
	return snapshotFlags.buildRequest()
}

type snapshotFlagSet struct {
	machine       *string
	depotAddr     *string
	configPath    *string
	acceptSecrets *bool
	capturedAt    *string
	maxSessions   *int
	force         *bool
	jsonOut       *bool
	progress      *string
	sourceFlags   multiFlag
	sessionFlags  multiFlag
}

func registerSnapshotFlags(fs *flag.FlagSet) *snapshotFlagSet {
	flags := &snapshotFlagSet{}
	flags.machine = fs.String("machine", "", "machine id")
	flags.depotAddr = fs.String("depot", "", "depot address")
	flags.configPath = fs.String("config", "", "JSONC config path")
	flags.acceptSecrets = fs.Bool("accept-secrets", false, "acknowledge raw snapshot privacy warning")
	flags.capturedAt = fs.String("captured-at", "", "capture timestamp (advanced deterministic testing)")
	flags.maxSessions = fs.Int("max-sessions", 0, "maximum number of discovered local sessions to snapshot (0 means all)")
	flags.force = fs.Bool("force", false, "bypass the capture cache and re-read every source file")
	flags.jsonOut = fs.Bool("json", false, "JSON output")
	flags.progress = fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json (stderr only)")
	fs.Var(&flags.sourceFlags, "source", "source spec type=path (repeatable)")
	fs.Var(&flags.sessionFlags, "session", "session id/path match to snapshot (repeatable)")
	return flags
}

func (f *snapshotFlagSet) buildRequest() (snapshotRequest, error) {
	req, err := buildSnapshotRequest(*f.configPath, *f.machine, *f.depotAddr, *f.acceptSecrets, *f.capturedAt, f.sourceFlags, f.sessionFlags, *f.maxSessions)
	if err != nil {
		return snapshotRequest{}, err
	}
	req.JSON = *f.jsonOut
	req.ProgressSetting = *f.progress
	req.Force = *f.force
	return req, nil
}

func buildSnapshotRequest(configPath, machine, depotAddr string, acceptSecrets bool, capturedAt string, sourceFlags, sessionFlags multiFlag, maxSessions int) (snapshotRequest, error) {
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
		return snapshotRequest{}, errPrivacyAcknowledgement
	}
	if maxSessions < 0 {
		return snapshotRequest{}, errors.New("--max-sessions must be >= 0")
	}
	depotAddress, err := depot.AddressFromConfig(cfg.Depot)
	if err != nil {
		return snapshotRequest{}, err
	}
	if depotAddr != "" {
		parsed, err := depot.ParseExplicitAddress(depotAddr)
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
	return snapshotRequest{Config: cfg, DepotOverride: depotAddr, CapturedAt: capturedAt, SessionFilters: []string(sessionFlags), MaxSessions: maxSessions}, nil
}
