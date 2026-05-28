// aha-gen-ts regenerates clients/typescript/aha-mcp.ts from the MCP tool
// registry and Go struct shapes. Run after changing any tool or any of the
// returned types.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/adewale/aha/internal/mcp/codegen"
)

func main() {
	out := flag.String("out", "clients/typescript/aha-mcp.ts", "output path")
	flag.Parse()
	if err := os.MkdirAll(dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, codegen.Generate(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}

func dir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
