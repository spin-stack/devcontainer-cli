package cli

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGenerateDocsCommandsProjectFolder(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFeature(t, ".", "decoy", `{"id":"decoy","version":"1.0.0","name":"Decoy"}`)
	commands := map[string]struct {
		cmd      func() *cobra.Command
		metadata string
		flags    []string
	}{
		"features":  {realFeaturesGenerateDocsCmd, "devcontainer-feature.json", []string{"--namespace", "o/r"}},
		"templates": {realTemplatesGenerateDocsCmd, "devcontainer-template.json", nil},
	}
	for name, c := range commands {
		for _, positional := range []bool{true, false} {
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, c.metadata), []byte(`{"id":"solo","version":"1.0.0","name":"Solo"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"--project-folder", project}
			if positional {
				args = []string{project}
			}
			cmd := c.cmd()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(append(args, c.flags...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s %v: %v", name, args, err)
			}
			if _, err := os.Stat(filepath.Join(project, "README.md")); err != nil {
				t.Errorf("%s %v: README not generated in the project folder: %v", name, args, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join("decoy", "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the working directory was documented: %v", err)
	}
}

func runFeaturesTestCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := realFeaturesTestCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestFeaturesTestCommandFlagConflicts(t *testing.T) {
	empty := t.TempDir()
	conflicts := map[string][]string{
		"Cannot combine --global-scenarios-only and --features":       {"--global-scenarios-only", "--features", "a"},
		"Cannot combine --skip-scenarios and --global-scenarios-only": {"--global-scenarios-only", "--skip-scenarios"},
	}
	for want, args := range conflicts {
		if _, err := runFeaturesTestCmd(t, append(args, "-p", empty)...); err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
	for _, args := range [][]string{{"--features", "a"}, {"--global-scenarios-only"}, {"--skip-scenarios"}} {
		if _, err := runFeaturesTestCmd(t, append(args, "-p", empty)...); exitCode(err) != 1 {
			t.Errorf("%v: err = %v, want the collection check's exit 1", args, err)
		}
	}
}

func TestFeaturesTestCommandTarget(t *testing.T) {
	project, target := t.TempDir(), t.TempDir()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{target, "-p", project}, target},
		{[]string{".", "-p", project}, project},
		{[]string{"-p", project}, project},
	}
	for _, c := range cases {
		out, err := runFeaturesTestCmd(t, c.args...)
		if exitCode(err) != 1 {
			t.Errorf("%v: err = %v, want exit 1", c.args, err)
		}
		if want := "[-] Folder '" + c.want + "' does not contain the required 'src' and 'test' folders.\n"; !strings.HasSuffix(out, want) {
			t.Errorf("%v: stdout = %q, want it to end with %q", c.args, out, want)
		}
	}
}

func TestOrderedObjectKeys(t *testing.T) {
	raw := `{"z":{"x":"y","k":{"deep":[1,{"q":"r"}]}},"a":["s",{"t":"u"}],"m":"v","b":null}`
	if got, want := orderedObjectKeys(json.RawMessage(raw)), []string{"z", "a", "m", "b"}; !slices.Equal(got, want) {
		t.Errorf("keys = %q, want %q", got, want)
	}
	for _, raw := range []string{``, `["a","b"]`, `"a"`, `{`} {
		if got := orderedObjectKeys(json.RawMessage(raw)); len(got) != 0 {
			t.Errorf("%s: keys = %q, want none", raw, got)
		}
	}
}

func TestCreateTarArchiveEntries(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "install.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "out.tgz")
	if err := createTarArchive(archive, src); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		if h.Uname != "" || h.Gname != "" {
			t.Errorf("%s: uname %q, gname %q, want both empty", h.Name, h.Uname, h.Gname)
		}
		if h.Name == "./sub/install.sh" {
			if b, _ := io.ReadAll(tr); string(b) != "echo hi\n" {
				t.Errorf("install.sh = %q", b)
			}
		}
	}
	if want := []string{"./", "./sub/", "./sub/install.sh"}; !slices.Equal(names, want) {
		t.Errorf("entries = %q, want %q", names, want)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(data)%512 != 0 || len(data) < 1024 || !bytes.Equal(data[len(data)-1024:], make([]byte, 1024)) {
		t.Errorf("archive of %d bytes does not end in the tar footer", len(data))
	}
}
