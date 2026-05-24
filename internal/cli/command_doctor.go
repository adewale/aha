package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/model"
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
	if *jsonOut {
		var ads []map[string]any
		for _, name := range names {
			ad := adapters.Builtins()[name]
			ads = append(ads, map[string]any{"name": name, "version": ad.Version(), "capabilities": ad.Capabilities(), "default_roots": ad.DefaultRoots()})
		}
		return writeJSON(stdout, map[string]any{"version": model.Version, "config": config.DefaultPath(), "adapters": ads, "depot": depotDiag, "next": []string{"aha init --accept-secrets", "aha refresh", "aha search <query>"}})
	}
	fmt.Fprintf(stdout, "aha: %s\nconfig: %s\n", model.Version, config.DefaultPath())
	fmt.Fprintf(stdout, "depot: %s:%s ok=%v\n", depotDiag["type"], depotDiag["location"], depotDiag["ok"])
	for _, name := range names {
		ad := adapters.Builtins()[name]
		fmt.Fprintf(stdout, "adapter: %s version=%s capabilities=%s\n", name, ad.Version(), mustJSON(ad.Capabilities()))
	}
	return nil
}

func doctorDepot(cfg model.Config, override string, cfgErr error) map[string]any {
	out := map[string]any{"ok": false}
	if cfgErr != nil {
		out["error"] = cfgErr.Error()
		return out
	}
	drv, err := depotDriverForConfig(cfg, override)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	addr := drv.Address()
	out["type"] = addr.Type
	out["location"] = addr.Location
	report, err := drv.Verify(context.Background(), false)
	if err != nil {
		out["error"] = err.Error()
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
