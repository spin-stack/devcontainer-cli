package config

import "testing"

func TestResolveKeepsAnEmptyTagAndResolvesTheRest(t *testing.T) {
	ctx := SubstitutionContext{HostSubContext: HostSubContext{LocalWorkspaceFolder: "/home/me/proj"}}
	got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${}-${localWorkspaceFolderBasename}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "${}-proj" {
		t.Errorf("Resolve = %q, want ${}-proj", got)
	}
}

func TestResolveContainerEnvIgnoresCaseOnlyOnWindows(t *testing.T) {
	for platform, want := range map[string]string{"win32": "/bin", "linux": ""} {
		ctx := SubstitutionContext{
			HostSubContext: HostSubContext{Platform: platform},
			ContainerEnv:   map[string]string{"Path": "/bin"},
		}
		got, err := NewVariableResolver().Resolve(ctx, PhaseContainer, "${containerEnv:PATH}")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: Resolve = %q, want %q", platform, got, want)
		}
	}
}
