package cli

import (
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
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names := make([]string, 0, len(adapters.Builtins()))
	for n := range adapters.Builtins() {
		names = append(names, n)
	}
	sort.Strings(names)
	if *jsonOut {
		var ads []map[string]any
		for _, name := range names {
			ad := adapters.Builtins()[name]
			ads = append(ads, map[string]any{"name": name, "version": ad.Version(), "capabilities": ad.Capabilities(), "default_roots": ad.DefaultRoots()})
		}
		return writeJSON(stdout, map[string]any{"version": model.Version, "config": config.DefaultPath(), "adapters": ads, "next": []string{"aha init --accept-secrets", "aha refresh", "aha search <query>"}})
	}
	fmt.Fprintf(stdout, "aha: %s\nconfig: %s\n", model.Version, config.DefaultPath())
	for _, name := range names {
		ad := adapters.Builtins()[name]
		fmt.Fprintf(stdout, "adapter: %s version=%s capabilities=%s\n", name, ad.Version(), mustJSON(ad.Capabilities()))
	}
	return nil
}
