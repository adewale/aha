// aha-gen-version-audit regenerates the source-version audit from the
// committed adapter fixtures. It writes both renderings to explicitly selected
// outputs and never infers a repository path.
//
// Run after vendoring a corpus, adding a fixture, or changing an adapter's
// version signal, and commit the diff. The diff is the point: it names the
// versions the new evidence adds and the key paths that arrived with them.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/adapters/fixtureaudit"
)

func main() {
	fixtures := flag.String("fixtures", "", "adapter fixture root (required)")
	jsonOut := flag.String("json", "", "output path for the JSON audit (required)")
	markdownOut := flag.String("markdown", "", "output path for the rendered audit (required)")
	flag.Parse()
	for name, value := range map[string]string{"fixtures": *fixtures, "json": *jsonOut, "markdown": *markdownOut} {
		if value == "" {
			fmt.Fprintf(os.Stderr, "error: -%s is required\n", name)
			os.Exit(2)
		}
	}

	audit, err := fixtureaudit.BuildAudit(*fixtures, fixtureaudit.ReadersFrom(adapters.Builtins()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body, err := audit.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := write(*jsonOut, body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := write(*markdownOut, []byte(audit.Markdown())); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *jsonOut)
	fmt.Println("wrote", *markdownOut)
}

func write(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
