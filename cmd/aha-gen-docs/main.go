// aha-gen-docs writes generated command reference documentation to an
// explicitly selected output. It never infers or overwrites a repository path.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/cli"
)

func main() {
	out := flag.String("out", "", "output path (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "error: -out is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(cli.GenerateCommandsMarkdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}
