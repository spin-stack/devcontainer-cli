// Package doctor implements the host-preflight diagnostics behind the
// `devcontainer check` and `devcontainer setup` commands. It probes the local
// Docker/BuildKit environment for the capabilities the CLI relies on (a running
// daemon, buildx, build-cache export, Compose v2, free disk) and reports a
// ✔/⚠/✖ result per check, each with a remediation hint.
//
// The whole package runs external commands through the exec.Runner seam so the
// checks can be unit-tested with a fake runner instead of a real Docker daemon.
package doctor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devcontainers/cli/internal/exec"
)

// Status is the outcome of a single check, ordered by severity so a Report's
// overall status is simply the maximum across its results.
type Status int

const (
	// StatusOK means the capability is present and usable.
	StatusOK Status = iota
	// StatusWarn means a non-fatal capability is missing or degraded; the CLI
	// still works for the common path but some features are unavailable.
	StatusWarn
	// StatusFail means a capability the CLI cannot operate without is missing.
	StatusFail
)

// String returns the lowercase token used in JSON output and the state file.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

// Symbol returns the glyph used in the human-readable table.
func (s Status) Symbol() string {
	switch s {
	case StatusOK:
		return "✔"
	case StatusWarn:
		return "⚠"
	case StatusFail:
		return "✖"
	default:
		return "?"
	}
}

// MarshalJSON encodes the status as its lowercase token.
func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes the lowercase token (unknown tokens decode to StatusWarn
// so an older/newer state file never crashes a load).
func (s *Status) UnmarshalJSON(b []byte) error {
	var tok string
	if err := json.Unmarshal(b, &tok); err != nil {
		return err
	}
	switch tok {
	case "ok":
		*s = StatusOK
	case "fail":
		*s = StatusFail
	default:
		*s = StatusWarn
	}
	return nil
}

// Result is the outcome of one named check.
type Result struct {
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Summary     string `json:"summary"`
	Remediation string `json:"remediation,omitempty"`
	// Fixable is true when `devcontainer setup` can remediate this check
	// automatically (no manual/sudo step required).
	Fixable bool `json:"fixable,omitempty"`
}

// Report is the full result of a check run and the exact shape persisted to the
// state file.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	CLIVersion    string    `json:"cli_version"`
	CheckedAt     time.Time `json:"checked_at"`
	Overall       Status    `json:"overall"`
	Results       []Result  `json:"results"`
}

// schemaVersion is bumped when the persisted Report shape changes incompatibly.
const schemaVersion = 1

// Env carries the injectable dependencies of a check run. The zero value is
// usable (defaults to a real OS-backed runner, `docker`, statfs disk probe and
// os.TempDir), but tests populate it to avoid touching a real daemon.
type Env struct {
	// Runner runs docker subcommands. Defaults to exec.OSRunner{}.
	Runner exec.Runner
	// DockerPath is the docker binary. Defaults to "docker".
	DockerPath string
	// DiskFree returns the free bytes available at path. Defaults to statfs.
	DiskFree func(path string) (uint64, error)
	// ProbeDir is the base directory for the cache-export probe's temp context.
	// Defaults to os.TempDir().
	ProbeDir string
	// CLIVersion stamps the Report. Defaults to product.GetConfig().Version.
	CLIVersion string
}

func (e *Env) runner() exec.Runner {
	if e.Runner != nil {
		return e.Runner
	}
	return exec.OSRunner{}
}

func (e *Env) dockerPath() string {
	if e.DockerPath != "" {
		return e.DockerPath
	}
	return "docker"
}

// checks is the ordered list of diagnostics run by Run. Daemon first (its
// failure explains the rest), then the build/runtime capabilities.
var checks = []func(context.Context, *Env) Result{
	checkDockerDaemon,
	checkBuildx,
	checkCacheExport,
	checkComposeV2,
	checkDiskSpace,
}

// Run executes every check in order and returns a Report. It never returns an
// error: a failed probe is a Result with StatusFail, not a Go error, so the
// caller always gets a complete report to render and persist.
func Run(ctx context.Context, env *Env) Report {
	if env == nil {
		env = &Env{}
	}
	results := make([]Result, 0, len(checks))
	overall := StatusOK
	for _, c := range checks {
		r := c(ctx, env)
		if r.Status > overall {
			overall = r.Status
		}
		results = append(results, r)
	}
	return Report{
		SchemaVersion: schemaVersion,
		CLIVersion:    env.CLIVersion,
		CheckedAt:     time.Now().UTC(),
		Overall:       overall,
		Results:       results,
	}
}
