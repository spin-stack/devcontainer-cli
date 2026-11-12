package config

import "testing"

func TestResolveHostPhaseResolvesTheContainerWorkspaceFolderFirst(t *testing.T) {
	ctx := SubstitutionContext{HostSubContext: HostSubContext{
		LocalWorkspaceFolder:     "/home/me/proj",
		ContainerWorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
	}}
	got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${containerWorkspaceFolder}/src")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspaces/proj/src" {
		t.Errorf("host phase = %q, want /workspaces/proj/src", got)
	}

	ctx.ContainerWorkspaceFolder = ""
	if got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${containerWorkspaceFolder}"); err != nil || got != "${containerWorkspaceFolder}" {
		t.Errorf("no container folder = %q, %v; want the tag unchanged", got, err)
	}
}
