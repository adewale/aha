// aha-gen-ts regenerates clients/typescript/aha-mcp.ts from the MCP tool
// registry and Go struct shapes. Run after changing any tool or any of the
// returned types.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/mcp/codegen"
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
	if err := os.WriteFile(*out, codegen.Generate(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if w := codegen.Warnings(); w != "" {
		fmt.Fprint(os.Stderr, w)
	}
	fmt.Println("wrote", *out)
}
