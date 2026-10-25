package cli

import (
	"testing"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

// An image neither local nor in a registry is pulled five times before the
// inspection fails, as TS retry({ maxRetries: 5 }) tries it.
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
