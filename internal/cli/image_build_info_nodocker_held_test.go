package cli

import (
	"testing"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

func TestNewImageInspectorPullsFiveTimes(t *testing.T) {
	const name = "Not A Valid Name"
	api := &fakeEngineAPI{}
	insp := newImageInspector(t.Context(), log.Null, &docker.EngineClient{API: api, Log: log.Null})
	insp.pullInterval = 0
	if got, err := insp.inspect(t.Context(), name); err == nil {
		t.Fatalf("inspect = %+v, want the pull failure", got)
	}
	if len(api.pulls) != 5 {
		t.Errorf("pulls = %d, want 5", len(api.pulls))
	}
}
