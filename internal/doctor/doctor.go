package doctor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devcontainers/cli/internal/exec"
)

type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusFail
)

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

func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

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

type Result struct {
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Summary     string `json:"summary"`
	Remediation string `json:"remediation,omitempty"`
	Fixable     bool   `json:"fixable,omitempty"`
}

type Report struct {
	SchemaVersion int       `json:"schema_version"`
	CLIVersion    string    `json:"cli_version"`
	CheckedAt     time.Time `json:"checked_at"`
	Overall       Status    `json:"overall"`
	Results       []Result  `json:"results"`
}

const schemaVersion = 1

type Env struct {
	Runner             exec.Runner
	DockerPath         string
	DiskFree           func(path string) (uint64, error)
	ProbeDir           string
	CLIVersion         string
	SELinuxEnforcePath string
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

var checks = []func(context.Context, *Env) Result{
	checkDockerDaemon,
	checkBuildx,
	checkCacheExport,
	checkComposeV2,
	checkDiskSpace,
	checkSELinux,
}

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
