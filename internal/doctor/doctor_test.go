package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptRunner is a programmable exec.Runner: fn decides the response from the
// command's args, and every call is recorded for assertions.
type scriptRunner struct {
	fn    func(args []string) (stdout, stderr []byte, code int, err error)
	calls [][]string
}

func (s *scriptRunner) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, int, error) {
	s.calls = append(s.calls, args)
	return s.fn(args)
}

// healthyDocker answers every probe as a fully-configured host.
func healthyDocker(args []string) ([]byte, []byte, int, error) {
	switch {
	case has(args, "info") && has(args, "{{.ServerVersion}}"):
		return []byte("27.0.3\n"), nil, 0, nil
	case has(args, "info") && has(args, "{{.DockerRootDir}}"):
		return []byte("/var/lib/docker\n"), nil, 0, nil
	case has(args, "buildx") && has(args, "version"):
		return []byte("github.com/docker/buildx v0.16.0\n"), nil, 0, nil
	case has(args, "buildx") && has(args, "build"):
		return nil, nil, 0, nil
	case has(args, "compose") && has(args, "version"):
		return []byte("2.29.0\n"), nil, 0, nil
	default:
		return nil, nil, 0, nil
	}
}

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func bigDisk(string) (uint64, error) { return 100 << 30, nil }

func TestRunAllHealthy(t *testing.T) {
	env := &Env{Runner: &scriptRunner{fn: healthyDocker}, DiskFree: bigDisk, ProbeDir: t.TempDir()}
	rep := Run(t.Context(), env)

	if rep.Overall != StatusOK {
		t.Fatalf("overall = %s, want ok; results=%+v", rep.Overall, rep.Results)
	}
	if len(rep.Results) != len(checks) {
		t.Fatalf("got %d results, want %d", len(rep.Results), len(checks))
	}
	for _, r := range rep.Results {
		if r.Status != StatusOK {
			t.Errorf("check %s = %s (%s), want ok", r.Name, r.Status, r.Summary)
		}
	}
}

func TestDockerDaemonDownIsFatal(t *testing.T) {
	env := &Env{
		Runner: &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
			if has(args, "info") && has(args, "{{.ServerVersion}}") {
				return nil, []byte("Cannot connect to the Docker daemon"), 1, nil
			}
			return healthyDocker(args)
		}},
		DiskFree: bigDisk, ProbeDir: t.TempDir(),
	}
	rep := Run(t.Context(), env)
	if rep.Overall != StatusFail {
		t.Fatalf("overall = %s, want fail", rep.Overall)
	}
	if got := findResult(t, rep, "docker-daemon"); got.Status != StatusFail || got.Remediation == "" {
		t.Fatalf("docker-daemon = %+v, want fail with remediation", got)
	}
}

func TestDockerBinaryMissing(t *testing.T) {
	env := &Env{
		Runner: &scriptRunner{fn: func([]string) ([]byte, []byte, int, error) {
			return nil, nil, -1, errors.New("exec: \"docker\": not found")
		}},
		DiskFree: bigDisk, ProbeDir: t.TempDir(),
	}
	rep := Run(t.Context(), env)
	got := findResult(t, rep, "docker-daemon")
	if got.Status != StatusFail || !strings.Contains(got.Summary, "not found") {
		t.Fatalf("docker-daemon = %+v, want fail 'not found'", got)
	}
}

func TestCacheExportUnsupportedIsFixableWarn(t *testing.T) {
	env := &Env{
		Runner: &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
			if has(args, "buildx") && has(args, "build") {
				return nil, []byte("ERROR: Cache export is not supported for the docker driver.\n"), 1, nil
			}
			return healthyDocker(args)
		}},
		DiskFree: bigDisk, ProbeDir: t.TempDir(),
	}
	rep := Run(t.Context(), env)
	got := findResult(t, rep, "build-cache-export")
	if got.Status != StatusWarn || !got.Fixable {
		t.Fatalf("build-cache-export = %+v, want fixable warn", got)
	}
	if rep.Overall != StatusWarn {
		t.Fatalf("overall = %s, want warn (no hard failures)", rep.Overall)
	}
	if !strings.Contains(got.Summary, "Cache export is not supported") {
		t.Errorf("summary missing daemon error: %q", got.Summary)
	}
}

func TestBuildxAndComposeMissingAreWarn(t *testing.T) {
	env := &Env{
		Runner: &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
			if has(args, "buildx") { // version + build both unavailable
				return nil, []byte("unknown command"), 1, nil
			}
			if has(args, "compose") {
				return nil, []byte("unknown command"), 1, nil
			}
			return healthyDocker(args)
		}},
		DiskFree: bigDisk, ProbeDir: t.TempDir(),
	}
	rep := Run(t.Context(), env)
	for _, name := range []string{"buildx", "compose-v2"} {
		if got := findResult(t, rep, name); got.Status != StatusWarn {
			t.Errorf("%s = %s, want warn", name, got.Status)
		}
	}
}

func TestDiskSpaceLowWarns(t *testing.T) {
	env := &Env{
		Runner:   &scriptRunner{fn: healthyDocker},
		DiskFree: func(string) (uint64, error) { return 1 << 30, nil }, // 1 GiB < 5 GiB floor
		ProbeDir: t.TempDir(),
	}
	rep := Run(t.Context(), env)
	got := findResult(t, rep, "disk-space")
	if got.Status != StatusWarn || !strings.Contains(got.Summary, "low free disk") {
		t.Fatalf("disk-space = %+v, want low-space warn", got)
	}
}

func findResult(t *testing.T, rep Report, name string) Result {
	t.Helper()
	for _, r := range rep.Results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result named %q in %+v", name, rep.Results)
	return Result{}
}
