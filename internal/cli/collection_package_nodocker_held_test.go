package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// A Template is a dockerfile Template when its devcontainer.json names the
// legacy dockerFile or a build.dockerfile, either one alone (TS
// isDockerFileConfig); image wins over both, and a config with none of the
// three is refused.
func TestAddTemplatePropsType(t *testing.T) {
	for name, tc := range map[string]struct {
		config, want string
	}{
		"image":                    {`{"image":"img","dockerFile":"Dockerfile"}`, "image"},
		"legacy dockerFile":        {`{"dockerFile":"Dockerfile"}`, "dockerfile"},
		"build.dockerfile":         {`{"build":{"dockerfile":"Dockerfile"}}`, "dockerfile"},
		"compose":                  {`{"dockerComposeFile":"compose.yml","service":"app"}`, "dockerCompose"},
		"build without dockerfile": {`{"build":{"context":"."}}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, ".devcontainer.json"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			jsonPath := filepath.Join(src, "devcontainer-template.json")
			if err := os.WriteFile(jsonPath, []byte(`{"id":"t"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			ok := addTemplateProps(log.Null, src, jsonPath)
			if ok != (tc.want != "") {
				t.Fatalf("addTemplateProps = %v, want %v", ok, tc.want != "")
			}
			if !ok {
				return
			}
			data, err := os.ReadFile(jsonPath)
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Type string }
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Type != tc.want {
				t.Errorf("type = %q, want %q", got.Type, tc.want)
			}
		})
	}
}
