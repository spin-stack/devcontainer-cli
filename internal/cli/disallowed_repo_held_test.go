package cli

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCacheFolderNamesTheLinuxUser(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the per-user folder is Linux's")
	}
	u, err := user.Current()
	if err != nil || u.Username == "" {
		t.Skipf("no current user name: %v", err)
	}
	if got, want := cacheFolder(), filepath.Join(os.TempDir(), "devcontainercli-"+u.Username); got != want {
		t.Errorf("cacheFolder() = %q, want %q", got, want)
	}
}
