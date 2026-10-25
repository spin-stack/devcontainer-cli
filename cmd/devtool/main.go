// Command devtool is the repo's CI/dev helper, replacing the former scripts/*.sh.
// Its commands live in internal/devtools (devtools.Main), which wires stdin/stdout,
// git, `go tool`, and docker around the pure, unit-tested logic there. Invoked
// from the Taskfile and the CI workflow (e.g. `go run ./cmd/devtool coverage-gate
// coverage.out TOTAL=48`).
package main

import (
	"os"

	"github.com/devcontainers/cli/internal/devtools"
)

func main() {
	os.Exit(devtools.Main(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
