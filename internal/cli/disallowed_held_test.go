package cli

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
)

// The workspace's container is stopped, with no grace period, only when it is
// found and running; the result says whether it was stopped.
func TestStopRunningContainer(t *testing.T) {
	running := container.InspectResponse{State: &container.State{Running: true}}
	exited := container.InspectResponse{State: &container.State{Running: false}}
	for name, tc := range map[string]struct {
		api         *fakeDockerAPI
		want        bool
		wantStopped []string
	}{
		"running":          {api: &fakeDockerAPI{containers: []string{"c1"}, inspect: map[string]container.InspectResponse{"c1": running}}, want: true, wantStopped: []string{"c1"}},
		"first of several": {api: &fakeDockerAPI{containers: []string{"c1", "c2"}, inspect: map[string]container.InspectResponse{"c1": running, "c2": exited}}, want: true, wantStopped: []string{"c1"}},
		"none":             {api: &fakeDockerAPI{}},
		"list fails":       {api: &fakeDockerAPI{containers: []string{"c1"}, inspect: map[string]container.InspectResponse{"c1": running}, listErr: errors.New("down")}},
		"inspect fails":    {api: &fakeDockerAPI{containers: []string{"c1"}, inspect: map[string]container.InspectResponse{"c1": running}, inspectErr: errors.New("gone")}},
		"no state":         {api: &fakeDockerAPI{containers: []string{"c1"}, inspect: map[string]container.InspectResponse{"c1": {}}}},
		"not running":      {api: &fakeDockerAPI{containers: []string{"c1", "c2"}, inspect: map[string]container.InspectResponse{"c1": exited, "c2": running}}},
		"stop fails":       {api: &fakeDockerAPI{containers: []string{"c1"}, inspect: map[string]container.InspectResponse{"c1": running}, stopErr: errors.New("busy")}, wantStopped: []string{"c1"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := stopRunningContainer(&docker.EngineClient{API: tc.api}, []string{"devcontainer.local_folder=/w"})(t.Context())
			if got != tc.want || !reflect.DeepEqual(tc.api.stopped, tc.wantStopped) {
				t.Errorf("stopped = %v (%v), want %v (%v)", got, tc.api.stopped, tc.want, tc.wantStopped)
			}
			for _, timeout := range tc.api.stopTimeouts {
				if timeout != 0 {
					t.Errorf("stop timeout = %d, want 0", timeout)
				}
			}
		})
	}
}

// Without Features there is nothing to check, and the control manifest is
// neither fetched nor cached.
func TestEnforceDisallowedFeaturesWithoutFeaturesFetchesNothing(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for name, cfg := range map[string]*config.DevContainer{"no config": nil, "no features": {Features: map[string]any{}}} {
		if err := enforceDisallowedFeatures(t.Context(), cfg, "", log.Null, nil); err != nil {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := os.Stat(cacheFolder()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cache folder %s: %v, want it not created", cacheFolder(), err)
	}
}
