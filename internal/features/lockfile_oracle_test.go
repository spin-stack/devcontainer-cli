package features

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// TestTSOracleReadLockfile asserts ReadLockfile against the reference's
// readLockfile over the same file (go tool task testdata:ts-oracle): what it
// makes of the file, or the message of the error it throws, which for JSON
// that does not parse is V8's JSON.parse message.
func TestTSOracleReadLockfile(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/lockfile-read.json", map[string]oracletest.Func{
		"readLockfile": func(args []json.RawMessage) (any, error) {
			var content any
			if err := oracletest.Args(args, &content); err != nil {
				return nil, err
			}
			dir := t.TempDir()
			lockfilePath := filepath.Join(dir, "devcontainer-lock.json")
			var err error
			switch c := content.(type) {
			case string:
				err = os.WriteFile(lockfilePath, []byte(c), 0o644)
			case map[string]any:
				if b64, ok := c["base64"].(string); ok {
					data, derr := base64.StdEncoding.DecodeString(b64)
					if derr != nil {
						return nil, derr
					}
					err = os.WriteFile(lockfilePath, data, 0o644)
				} else if c["directory"] == true {
					err = os.Mkdir(lockfilePath, 0o755)
				}
			}
			if err != nil {
				return nil, err
			}
			lockfile, initLockfile, err := ReadLockfile(filepath.Join(dir, "devcontainer.json"))
			switch {
			case err != nil:
				// The oracle writes its temporary folder as <dir>.
				return nil, errors.New(strings.ReplaceAll(err.Error(), dir, "<dir>"))
			case initLockfile:
				return "init", nil
			case lockfile != nil:
				return "lockfile", nil
			}
			return "none", nil
		},
	})
}
