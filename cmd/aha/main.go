package main

import (
	"os"

	"github.com/adewale/aha/internal/cli"
)

func main() {
	os.Exit(cli.RunMain(os.Args[1:], os.Stdout, os.Stderr))
}
