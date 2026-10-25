package features

import (
	"encoding/json"
	"testing"
)

// read-configuration reports each Feature set's sourceInformation as TS builds
// it in processFeatureIdentifier: type first, then the location, then the
// user's reference. Go field names must not leak into the output.
func TestSourceInformationJSONMatchesTS(t *testing.T) {
	tests := []struct {
		name string
		src  SourceInfo
		want string
	}{
		{
			name: "file-path",
			src:  &LocalSource{LocalPath: "./localFeatureA", ResolvedPath: "/ws/.devcontainer/localFeatureA", UserID: "./localFeatureA"},
			want: `{"type":"file-path","resolvedFilePath":"/ws/.devcontainer/localFeatureA","userFeatureId":"./localFeatureA"}`,
		},
		{
			name: "direct-tarball",
			src:  &TarballSource{TarballURI: "https://example.com/devcontainer-feature-go.tgz", UserID: "https://example.com/devcontainer-feature-go.tgz"},
			want: `{"type":"direct-tarball","tarballUri":"https://example.com/devcontainer-feature-go.tgz","userFeatureId":"https://example.com/devcontainer-feature-go.tgz"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(&Set{SourceInfo: tt.src})
			if err != nil {
				t.Fatal(err)
			}
			want := `{"sourceInformation":` + tt.want + `,"features":null}`
			if string(got) != want {
				t.Errorf("json = %s\nwant   %s", got, want)
			}
		})
	}
}
