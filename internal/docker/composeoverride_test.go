package docker

import "testing"

// The script is TS spawnDevContainer's byte for byte: the entrypoints' line is
// there even with no entrypoint, as an empty line between trap and exec.
func TestBuildEntrypointScript(t *testing.T) {
	for _, tt := range []struct {
		entrypoints []string
		want        string
	}{
		{nil, "echo Container started\ntrap \"exit 0\" 15\n\nexec \"$@\"\nwhile sleep 1 & wait $!; do :; done"},
		{[]string{"/usr/local/share/docker-init.sh"}, "echo Container started\ntrap \"exit 0\" 15\n/usr/local/share/docker-init.sh\nexec \"$@\"\nwhile sleep 1 & wait $!; do :; done"},
		{[]string{"/a.sh", "/b.sh"}, "echo Container started\ntrap \"exit 0\" 15\n/a.sh\n/b.sh\nexec \"$@\"\nwhile sleep 1 & wait $!; do :; done"},
	} {
		if got := BuildEntrypointScript(tt.entrypoints); got != tt.want {
			t.Errorf("BuildEntrypointScript(%q) =\n%q\nwant\n%q", tt.entrypoints, got, tt.want)
		}
	}
}
