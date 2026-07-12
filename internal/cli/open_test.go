package cli

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// TestDevcontainerFolderURI locks the vscode-remote dev-container URI format:
// vscode-remote://dev-container+<hex><containerFolder>, where <hex> is the
// hex-encoded JSON {hostPath, configFile:{scheme,path}}. The shape is verified
// against the working vscli launcher; a drift here silently breaks `open`.
func TestDevcontainerFolderURI(t *testing.T) {
	const (
		localWS     = "/home/me/project"
		localConfig = "/home/me/project/.devcontainer/devcontainer.json"
		container   = "/workspaces/project"
	)

	uri, err := devcontainerFolderURI(localWS, localConfig, container)
	if err != nil {
		t.Fatal(err)
	}

	const prefix = "vscode-remote://dev-container+"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("uri = %q, want prefix %q", uri, prefix)
	}
	rest := strings.TrimPrefix(uri, prefix)

	// The URI ends with the container workspace folder, verbatim.
	if !strings.HasSuffix(rest, container) {
		t.Fatalf("uri does not end with container folder %q: %q", container, uri)
	}
	hexStr := strings.TrimSuffix(rest, container)

	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("authority is not valid hex: %v", err)
	}

	var got devcontainerURIJSON
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoded authority is not the expected JSON: %v (%s)", err, raw)
	}
	if got.HostPath != localWS {
		t.Errorf("hostPath = %q, want %q", got.HostPath, localWS)
	}
	if got.ConfigFile.Scheme != "file" {
		t.Errorf("configFile.scheme = %q, want file", got.ConfigFile.Scheme)
	}
	if got.ConfigFile.Path != localConfig {
		t.Errorf("configFile.path = %q, want %q", got.ConfigFile.Path, localConfig)
	}
	// On Linux there is no authority component.
	if got.ConfigFile.Authority != "" {
		t.Errorf("configFile.authority = %q, want empty on Linux", got.ConfigFile.Authority)
	}

	// The exact hex is stable for a fixed input (guards accidental field reorder
	// or key renames that would change what VS Code receives).
	wantJSON := `{"hostPath":"/home/me/project","configFile":{"scheme":"file","path":"/home/me/project/.devcontainer/devcontainer.json"}}`
	if string(raw) != wantJSON {
		t.Errorf("payload JSON =\n  %s\nwant\n  %s", raw, wantJSON)
	}
}
