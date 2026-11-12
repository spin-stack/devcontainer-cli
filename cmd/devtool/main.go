package main

import (
	"os"

	"github.com/devcontainers/cli/internal/devtools"
)

func main() {
	os.Exit(devtools.Main(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
