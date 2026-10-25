package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// fakeDockerOnPath puts a `docker` first on PATH whose `ps` prints ids and
// whose `rm -f <id>` appends the id to the returned file, the way the runner's
// cleanup calls the real one.
func fakeDockerOnPath(t *testing.T, ids string) (psArgs, removed string) {
	t.Helper()
	dir := t.TempDir()
	psArgs, removed = filepath.Join(dir, "ps.args"), filepath.Join(dir, "removed")
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"ps) echo \"$@\" > '" + psArgs + "'; printf '" + ids + "' ;;\n" +
		"rm) echo \"$3\" >> '" + removed + "' ;;\n" +
		"esac\n"
	writeFile(t, filepath.Join(dir, "docker"), script, 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return psArgs, removed
}

func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func bufferLogger(buf *bytes.Buffer) log.Logger {
	return log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: buf})
}

// Cleanup removes every container the test run labelled, and says how many.
func TestCleanupTestContainersRemovesLabelledContainers(t *testing.T) {
	psArgs, removed := fakeDockerOnPath(t, `c1\n`)
	var logs bytes.Buffer
	cleanupTestContainers(bufferLogger(&logs))
	if got := readOrEmpty(t, psArgs); got != "ps -a --filter label=devcontainer.is_test_run=true --format {{.ID}}\n" {
		t.Errorf("docker ps args = %q", got)
	}
	if got := readOrEmpty(t, removed); got != "c1\n" {
		t.Errorf("removed = %q, want c1", got)
	}
	if !strings.Contains(logs.String(), "Cleaning up 1 test containers...") {
		t.Errorf("logs = %q", logs.String())
	}
}

// With no labelled containers there is nothing to remove or announce.
func TestCleanupTestContainersWithNone(t *testing.T) {
	_, removed := fakeDockerOnPath(t, ``)
	var logs bytes.Buffer
	cleanupTestContainers(bufferLogger(&logs))
	if got := readOrEmpty(t, removed); got != "" {
		t.Errorf("removed = %q, want nothing", got)
	}
	if strings.Contains(logs.String(), "Cleaning up") {
		t.Errorf("logs = %q, want no cleanup line", logs.String())
	}
}

// The workspace a test script enters is opened to every user, files and
// folders alike, so a non-root remote user can read it.
func TestMakeWorkspaceAccessible(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "feature")
	writeFile(t, filepath.Join(sub, "test.sh"), "true\n", 0o600)
	if err := os.Chmod(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	makeWorkspaceAccessible(dir)
	for _, p := range []string{dir, sub, filepath.Join(sub, "test.sh")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o777 {
			t.Errorf("%s mode = %v, want 0777", p, info.Mode().Perm())
		}
	}
}

// A test script runs through `<self> exec` in the test container, from the
// mounted workspace, with its output on this process's stdout and stderr.
func TestTestScriptCommand(t *testing.T) {
	cmd := testScriptCommand("/bin/devcontainer", "cid", "color/test.sh", "/tmp/devcontainer-test-1")
	want := []string{"/bin/devcontainer", "exec", "--container-id", "cid", "--", "bash", "-c",
		"cd /workspaces/devcontainer-test-1 && source dev-container-features-test-lib && source color/test.sh && reportResults"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
		t.Error("the script's output does not reach this process's stdout and stderr")
	}
}

// testCollection writes a collection with Features a and b (each with a
// test.sh), a _global scenario g, and a stray file in test/.
func testCollection(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, f := range []string{"a", "b"} {
		writeFile(t, filepath.Join(base, "src", f, "install.sh"), "#!/bin/sh\n", 0o755)
		writeFile(t, filepath.Join(base, "test", f, "test.sh"), "true\n", 0o755)
	}
	writeFile(t, filepath.Join(base, "test", "README.md"), "notes\n", 0o644)
	writeFile(t, filepath.Join(base, "test", "_global", "scenarios.json"), `{"g":{"image":"alpine"}}`, 0o644)
	writeFile(t, filepath.Join(base, "test", "_global", "g.sh"), "true\n", 0o755)
	return base
}

func runTestCommand(t *testing.T, collection string, featuresList []string) (code int, stdout, logs string) {
	t.Helper()
	out := &captureOutput{}
	var buf bytes.Buffer
	code = runFeaturesTestCommand(out, bufferLogger(&buf), collection, featuresList, "",
		false, false, false, false, "alpine", "", false, true, false, nil)
	return code, out.out.String(), buf.String()
}

// reportedNames lists the quoted names of the report's result lines, in order.
func reportedNames(stdout string) []string {
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		if _, rest, ok := strings.Cut(line, "'"); ok && strings.Contains(line, ":") && !strings.Contains(line, "Folder") {
			name, _, _ := strings.Cut(rest, "'")
			names = append(names, name)
		}
	}
	return names
}

// Without --features every Feature folder under test/ is tested (not _global,
// not a stray file), then the global scenarios; containers are cleaned up.
func TestRunFeaturesTestCommandDiscoversFeatures(t *testing.T) {
	_, removed := fakeDockerOnPath(t, `leftover\n`)
	fakeTestContainers(t, nil, nil, func(string, string) bool { return true })
	code, stdout, _ := runTestCommand(t, testCollection(t), nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, stdout)
	}
	if got, want := reportedNames(stdout), []string{"a", "b", "g"}; !slices.Equal(got, want) {
		t.Errorf("reported %q, want %q\n%s", got, want, stdout)
	}
	if got := readOrEmpty(t, removed); got != "leftover\n" {
		t.Errorf("removed = %q, want the labelled container", got)
	}
}

// --features tests exactly the named Features, and skips the global scenarios.
func TestRunFeaturesTestCommandNamedFeatures(t *testing.T) {
	fakeDockerOnPath(t, ``)
	fakeTestContainers(t, nil, nil, func(string, string) bool { return true })
	code, stdout, _ := runTestCommand(t, testCollection(t), []string{"a"})
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, stdout)
	}
	if got, want := reportedNames(stdout), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("reported %q, want %q\n%s", got, want, stdout)
	}
}

// A collection without both src/ and test/ fails with exit 1 and TS's message
// on stdout, whichever of the two is missing.
func TestRunFeaturesTestCommandRequiresSrcAndTest(t *testing.T) {
	for _, present := range []string{"src", "test"} {
		base := t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, present), 0o755); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := runTestCommand(t, base, nil)
		want := "[-] Folder '" + base + "' does not contain the required 'src' and 'test' folders.\n"
		if code != 1 || !strings.HasSuffix(stdout, want) {
			t.Errorf("only %s/: exit = %d, stdout = %q", present, code, stdout)
		}
	}
}

// With no Feature folder to test, the command fails with exit 1.
func TestRunFeaturesTestCommandWithoutFeatures(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"src", filepath.Join("test", "_global")} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeTestContainers(t, nil, nil, func(string, string) bool { return true })
	code, stdout, logs := runTestCommand(t, base, nil)
	if code != 1 || !strings.Contains(logs, "No features to test.") || strings.Contains(stdout, "TEST REPORT") {
		t.Errorf("exit = %d, logs = %q, stdout = %q", code, logs, stdout)
	}
}

// A test/ folder that cannot be listed fails the command with exit 1.
func TestRunFeaturesTestCommandUnreadableTestFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists any folder")
	}
	base := testCollection(t)
	testDir := filepath.Join(base, "test")
	if err := os.Chmod(testDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(testDir, 0o755) })
	code, _, logs := runTestCommand(t, base, nil)
	if code != 1 || !strings.Contains(logs, "Failed to discover feature tests") {
		t.Errorf("exit = %d, logs = %q", code, logs)
	}
}
