package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

func cmdDoctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	depotAddr := fs.String("depot", "", "depot address to check")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names := make([]string, 0, len(adapters.Builtins()))
	for n := range adapters.Builtins() {
		names = append(names, n)
	}
	sort.Strings(names)
	cfg, cfgErr := config.Load(*configPath)
	depotDiag := doctorDepot(cfg, *depotAddr, cfgErr)
	sourceDiag := doctorSources(cfg, cfgErr)
	corpusDiag := doctorCorpus(cfg, cfgErr)
	if *jsonOut {
		var ads []map[string]any
		for _, name := range names {
			ad := adapters.Builtins()[name]
			ads = append(ads, map[string]any{"name": name, "version": ad.Version(), "capabilities": ad.Capabilities(), "default_roots": ad.DefaultRoots()})
		}
		return writeJSON(stdout, map[string]any{"version": model.Version, "config": config.DefaultPath(), "adapters": ads, "sources": sourceDiag, "corpus": corpusDiag, "depot": depotDiag, "next": []string{"aha init --accept-secrets", "aha refresh", "aha search <query>"}})
	}
	fmt.Fprintf(stdout, "aha: %s\nconfig: %s\n", model.Version, config.DefaultPath())
	fmt.Fprintf(stdout, "corpus: %s ok=%v\n", corpusDiag["path"], corpusDiag["ok"])
	fmt.Fprintf(stdout, "depot: %s:%s ok=%v\n", depotDiag["type"], depotDiag["location"], depotDiag["ok"])
	for _, name := range names {
		ad := adapters.Builtins()[name]
		fmt.Fprintf(stdout, "adapter: %s version=%s capabilities=%s\n", name, ad.Version(), mustJSON(ad.Capabilities()))
	}
	return nil
}

func doctorSources(cfg model.Config, cfgErr error) []map[string]any {
	if cfgErr != nil {
		return []map[string]any{{"ok": false, "error": cfgErr.Error()}}
	}
	registry := adapters.Builtins()
	var out []map[string]any
	for _, sc := range cfg.Sources {
		item := map[string]any{"type": sc.Type, "root": sc.Root, "enabled": sc.Enabled, "ok": false}
		ad, ok := registry[sc.Type]
		item["adapter_known"] = ok
		if !sc.Enabled {
			item["ok"] = true
			out = append(out, item)
			continue
		}
		if !ok {
			item["error"] = "unknown source adapter"
			item["hints"] = []string{"Check the source type; built-ins are claude-code, codex, and pi."}
			out = append(out, item)
			continue
		}
		root, err := paths.Expand(sc.Root)
		if err != nil {
			item["error"] = err.Error()
			out = append(out, item)
			continue
		}
		item["expanded_root"] = root
		st, err := os.Stat(root)
		if err != nil {
			item["exists"] = false
			item["error"] = err.Error()
			item["hints"] = []string{"Create the source history root, disable this source, or update its configured root."}
			out = append(out, item)
			continue
		}
		item["exists"] = true
		item["is_dir"] = st.IsDir()
		if !st.IsDir() {
			item["error"] = "source root is not a directory"
			out = append(out, item)
			continue
		}
		found, err := ad.Discover(context.Background(), model.SourceConfig{Type: sc.Type, Root: root, Enabled: true})
		if err != nil {
			item["error"] = err.Error()
			out = append(out, item)
			continue
		}
		item["session_files"] = len(found)
		item["ok"] = true
		out = append(out, item)
	}
	return out
}

func doctorCorpus(cfg model.Config, cfgErr error) map[string]any {
	out := map[string]any{"ok": false}
	if cfgErr != nil {
		out["error"] = cfgErr.Error()
		return out
	}
	root, err := paths.Expand(cfg.CorpusDir)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	if root == "" {
		root, _ = paths.Expand("~/.aha")
	}
	out["path"] = root
	if st, err := os.Stat(root); err != nil {
		out["exists"] = false
		out["hints"] = []string{"Run `aha refresh` or `aha ingest` to create the corpus."}
		return out
	} else {
		out["exists"] = true
		out["is_dir"] = st.IsDir()
	}
	dbPath := filepath.Join(root, "corpus.db")
	if _, err := os.Stat(dbPath); err != nil {
		out["db_exists"] = false
		out["hints"] = []string{"Run `aha refresh` or `aha ingest` to create corpus.db."}
		return out
	}
	out["db_exists"] = true
	store, err := corpus.OpenExisting(root)
	if err != nil {
		out["error"] = err.Error()
		out["hints"] = []string{"Check corpus path permissions and schema; run `aha status --json` for details."}
		return out
	}
	defer store.Close()
	stats, err := corpus.Status(store.DB, store.Root)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	out["ok"] = true
	for _, key := range []string{"bundles", "sessions", "entries", "messages", "artifacts", "images", "conflicts", "index_size_bytes"} {
		out[key] = stats[key]
	}
	return out
}

func doctorDepot(cfg model.Config, override string, cfgErr error) map[string]any {
	out := map[string]any{"ok": false}
	if cfgErr != nil {
		out["error"] = cfgErr.Error()
		return out
	}
	addr := depot.AddressFromConfig(cfg.Depot)
	if override != "" {
		parsed, err := depot.ParseAddress(override)
		if err != nil {
			out["error"] = err.Error()
			out["hints"] = []string{"Use depot addresses like local:/path or r2:bucket-name."}
			return out
		}
		addr = parsed
	}
	out["type"] = addr.Type
	out["location"] = addr.Location
	warnings := depotConfigWarnings(addr, cfg.Depot.R2)
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	drv, err := depotDriverForConfig(cfg, override)
	if err != nil {
		out["error"] = err.Error()
		out["hints"] = depotErrorHints(err)
		return out
	}
	report, err := drv.Verify(context.Background(), false)
	if err != nil {
		out["error"] = err.Error()
		out["hints"] = depotErrorHints(err)
		return out
	}
	out["ok"] = len(report.Problems) == 0
	out["bundles"] = report.Bundles
	out["catalogs"] = report.Catalogs
	if len(report.Problems) > 0 {
		out["problems"] = report.Problems
	}
	return out
}

func depotConfigWarnings(addr depot.Address, cfg model.R2DepotConfig) []string {
	if addr.Type != "r2" {
		return nil
	}
	var warnings []string
	bucket := addr.Location
	if strings.HasPrefix(bucket, "http://") || strings.HasPrefix(bucket, "https://") || strings.Contains(bucket, "/") || strings.Contains(bucket, ".r2.") {
		warnings = append(warnings, "depot address should be r2:BUCKET; put account/endpoint in AHA_R2_ACCOUNT_ID or AHA_R2_ENDPOINT")
	}
	if strings.Contains(strings.ToLower(bucket), "r2.dev") {
		warnings = append(warnings, "do not use public r2.dev URLs as the S3 endpoint; use https://<ACCOUNT_ID>.r2.cloudflarestorage.com")
	}
	if !looksLikeR2BucketName(bucket) {
		warnings = append(warnings, "R2 bucket names should be 3-63 chars of lowercase letters, numbers, and hyphens")
	}
	region := firstNonEmpty(os.Getenv("AHA_R2_REGION"), os.Getenv("R2_REGION"), cfg.Region)
	if region != "" && region != "auto" {
		warnings = append(warnings, "Cloudflare R2 S3 region should be auto")
	}
	endpoint := firstNonEmpty(os.Getenv("AHA_R2_ENDPOINT"), os.Getenv("R2_ENDPOINT"), cfg.Endpoint)
	if endpoint != "" {
		lower := strings.ToLower(endpoint)
		if strings.Contains(lower, "r2.dev") {
			warnings = append(warnings, "do not use public r2.dev URLs as the S3 endpoint; use https://<ACCOUNT_ID>.r2.cloudflarestorage.com")
		}
		if strings.HasPrefix(lower, "http://") && !strings.Contains(lower, "localhost") && !strings.Contains(lower, "127.0.0.1") {
			warnings = append(warnings, "use HTTPS for R2 endpoints")
		}
		if strings.Contains(lower, "r2.cloudflarestorage.com") && !strings.HasPrefix(lower, "https://") {
			warnings = append(warnings, "Cloudflare R2 endpoint should include https://")
		}
		if strings.Contains(lower, "amazonaws.com") {
			warnings = append(warnings, "endpoint looks like AWS S3; for R2 use the Cloudflare account endpoint or an explicit S3-compatible test endpoint")
		}
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AHA_R2_ACCESS_KEY_ID") == "" && os.Getenv("R2_ACCESS_KEY_ID") == "" {
		warnings = append(warnings, "AHA ignores AWS_ACCESS_KEY_ID; set AHA_R2_ACCESS_KEY_ID or R2_ACCESS_KEY_ID")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") != "" && os.Getenv("AHA_R2_SECRET_ACCESS_KEY") == "" && os.Getenv("R2_SECRET_ACCESS_KEY") == "" {
		warnings = append(warnings, "AHA ignores AWS_SECRET_ACCESS_KEY; set AHA_R2_SECRET_ACCESS_KEY or R2_SECRET_ACCESS_KEY")
	}
	return warnings
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func looksLikeR2BucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func depotErrorHints(err error) []string {
	msg := strings.ToLower(err.Error())
	var hints []string
	if strings.Contains(msg, "account id required") {
		hints = append(hints, "Set AHA_R2_ACCOUNT_ID/R2_ACCOUNT_ID, or set AHA_R2_ENDPOINT for jurisdiction-specific or fake-S3 endpoints.")
	}
	if strings.Contains(msg, "credentials required") {
		hints = append(hints, "Set AHA_R2_ACCESS_KEY_ID and AHA_R2_SECRET_ACCESS_KEY, or their R2_* aliases.")
	}
	if strings.Contains(msg, "accessdenied") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "403") {
		hints = append(hints, "Check that the R2 token is scoped to this bucket and has Object Read & Write permissions.")
	}
	if strings.Contains(msg, "nosuchbucket") || strings.Contains(msg, "notfound") || strings.Contains(msg, "404") {
		hints = append(hints, "Check the bucket name, account ID, and jurisdiction-specific endpoint.")
	}
	if strings.Contains(msg, "signature") || strings.Contains(msg, "invalidaccesskey") {
		hints = append(hints, "Check that access key, secret key, account ID, endpoint, and region=auto belong together.")
	}
	if strings.Contains(msg, "no such host") {
		hints = append(hints, "Check AHA_R2_ACCOUNT_ID/AHA_R2_ENDPOINT spelling; R2 endpoints look like https://<ACCOUNT_ID>.r2.cloudflarestorage.com.")
	}
	if len(hints) == 0 {
		hints = append(hints, "Run `aha depot verify --repair` for catalog/object mismatches; check R2 bucket privacy, token scope, endpoint, and region=auto.")
	}
	return hints
}
