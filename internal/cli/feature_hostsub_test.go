package cli

import (
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/features"
)

// upstream #308: a Feature's containerEnv/mounts must get the same host-side
// variable substitution as devcontainer.json's own values.
func TestSubstituteFeatureHostVars(t *testing.T) {
	hs := config.HostSubContext{
		Platform:             "linux",
		LocalWorkspaceFolder: "/home/me/proj",
		Env:                  map[string]string{"HOME": "/home/me"},
	}
	sets := []*features.Set{{
		Features: []features.Feature{{
			ContainerEnv: map[string]string{"CACHE": "${localEnv:HOME}/.cache", "WS": "${localWorkspaceFolder}/x"},
			Mounts:       []interface{}{"source=${localEnv:HOME}/.ssh,target=/root/.ssh,type=bind"},
		}},
	}}

	substituteFeatureHostVars(sets, hs)

	f := sets[0].Features[0]
	if got := f.ContainerEnv["CACHE"]; got != "/home/me/.cache" {
		t.Errorf("containerEnv CACHE = %q, want /home/me/.cache", got)
	}
	if got := f.ContainerEnv["WS"]; got != "/home/me/proj/x" {
		t.Errorf("containerEnv WS = %q, want /home/me/proj/x", got)
	}
	if got := f.Mounts[0].(string); got != "source=/home/me/.ssh,target=/root/.ssh,type=bind" {
		t.Errorf("mount = %q, want resolved localEnv:HOME", got)
	}
}
