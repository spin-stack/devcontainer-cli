package doctor

import (
	"errors"
	"slices"
	"testing"
)

// builderRunner answers `buildx inspect` with inspect and every other docker
// call with rest.
func builderRunner(inspectCode int, rest func() ([]byte, []byte, int, error)) *scriptRunner {
	return &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		if has(args, "inspect") {
			return nil, nil, inspectCode, nil
		}
		return rest()
	}}
}

func TestEnsureCacheBuilderActions(t *testing.T) {
	ok := func() ([]byte, []byte, int, error) { return nil, nil, 0, nil }
	tests := []struct {
		name   string
		run    *scriptRunner
		dryRun bool
		want   Action
	}{
		{"existing builder selected", builderRunner(0, ok), false, Action{Name: "build-cache-export", Applied: true,
			Message: `buildx builder "devcontainer" already exists; selecting it as the default`}},
		{"existing builder dry run", builderRunner(0, ok), true, Action{Name: "build-cache-export",
			Message: `buildx builder "devcontainer" already exists; selecting it as the default`}},
		{"existing builder use fails with stderr", builderRunner(0, func() ([]byte, []byte, int, error) {
			return nil, []byte("\nERROR: no such builder\nmore\n"), 1, nil
		}), false, Action{Name: "build-cache-export", Err: "ERROR: no such builder",
			Message: `buildx builder "devcontainer" already exists; selecting it as the default`}},
		{"existing builder use cannot run", builderRunner(0, func() ([]byte, []byte, int, error) {
			return nil, nil, 0, errors.New("exec: docker: not found")
		}), false, Action{Name: "build-cache-export", Err: "exec: docker: not found",
			Message: `buildx builder "devcontainer" already exists; selecting it as the default`}},
		{"existing builder use fails silently", builderRunner(0, func() ([]byte, []byte, int, error) {
			return nil, nil, 1, nil
		}), false, Action{Name: "build-cache-export", Err: "command failed",
			Message: `buildx builder "devcontainer" already exists; selecting it as the default`}},
		{"absent builder dry run", builderRunner(1, ok), true, Action{Name: "build-cache-export",
			Message: `create docker-container buildx builder "devcontainer" and select it as the default`}},
		{"absent builder created", builderRunner(1, ok), false, Action{Name: "build-cache-export", Applied: true,
			Message: `created and selected docker-container buildx builder "devcontainer"`}},
		{"absent builder create cannot run", builderRunner(1, func() ([]byte, []byte, int, error) {
			return nil, nil, 0, errors.New("exec: docker: not found")
		}), false, Action{Name: "build-cache-export", Err: "exec: docker: not found",
			Message: `create docker-container buildx builder "devcontainer" and select it as the default`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ensureCacheBuilder(t.Context(), &Env{Runner: tt.run}, tt.dryRun); got != tt.want {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// Only a fixable cache-export result is remediated automatically; anything else
// is reported for manual action, whatever its name or Fixable flag.
func TestSetupRemediatesOnlyAFixableCacheExport(t *testing.T) {
	sr := builderRunner(1, func() ([]byte, []byte, int, error) { return nil, nil, 0, nil })
	report := Report{Results: []Result{
		{Name: "build-cache-export", Status: StatusWarn, Remediation: "enable containerd"},
		{Name: "disk-space", Status: StatusWarn, Fixable: true, Remediation: "free space"},
	}}
	got := Setup(t.Context(), &Env{Runner: sr}, report, false)
	want := []Action{
		{Name: "build-cache-export", Message: "manual action required: enable containerd"},
		{Name: "disk-space", Message: "manual action required: free space"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if len(sr.calls) != 0 {
		t.Errorf("no builder may be touched; calls=%v", sr.calls)
	}
}
