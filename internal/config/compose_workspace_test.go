package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A Docker Compose configuration's container workspace folder is its own
// workspaceFolder or `/`, never /workspaces/<basename> (TS
// getWorkspaceConfiguration -> getRemoteWorkspaceFolder, dockerCompose.ts).
// ${containerWorkspaceFolder} resolves to the same folder, with or without a
// workspace.
func TestLoadComposeWorkspaceFolder(t *testing.T) {
	tests := []struct {
		name         string
		extra        string
		useWorkspace bool
		want         string
	}{
		{name: "no workspaceFolder", useWorkspace: true, want: "/"},
		{name: "no workspaceFolder and no workspace", want: "/"},
		{name: "explicit workspaceFolder", extra: `,"workspaceFolder":"/work"`, useWorkspace: true, want: "/work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := t.TempDir()
			dir := filepath.Join(ws, ".devcontainer")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "devcontainer.json")
			body := `{"dockerComposeFile":"docker-compose.yml","service":"app","postCreateCommand":"echo ${containerWorkspaceFolder}"` + tt.extra + `}`
			if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			folder := ""
			if tt.useWorkspace {
				folder = ws
			}
			res, err := LoadDevContainerConfig(folder, cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := res.WorkspaceConfig.WorkspaceFolder; got != tt.want {
				t.Errorf("workspaceFolder = %q, want %q", got, tt.want)
			}
			if got := res.WorkspaceConfig.WorkspaceMount; got != "" {
				t.Errorf("workspaceMount = %q, want none", got)
			}
			if got, want := res.Raw["postCreateCommand"], "echo "+tt.want; got != want {
				t.Errorf("postCreateCommand = %q, want %q", got, want)
			}
		})
	}
}
