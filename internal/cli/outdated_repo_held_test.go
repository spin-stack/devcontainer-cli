package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/spf13/cobra"
)

func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	// The root command silences usage and errors; the bare subcommand does not.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

// outdated reads the workspace given by --workspace-folder, and the current
// directory without it.
func TestOutdatedCmdWorkspaceFolderDefaultsToCwd(t *testing.T) {
	ws := writeOutdatedWorkspace(t, `{}`, "")
	t.Chdir(t.TempDir())
	if got, err := executeCmd(t, newOutdatedCmd(), "--workspace-folder", ws); err != nil || got != "Feature  Current  Wanted  Latest\n" {
		t.Errorf("--workspace-folder: err = %v, stdout = %q", err, got)
	}
	t.Chdir(ws)
	if got, err := executeCmd(t, newOutdatedCmd()); err != nil || got != "Feature  Current  Wanted  Latest\n" {
		t.Errorf("cwd: err = %v, stdout = %q", err, got)
	}
}

// addFeature publishes repo as a Feature whose devcontainer-feature.json is
// meta (JSON): its manifest carries meta as its metadata annotation and its
// layer is the Feature's tarball.
func (r *readRegistry) addFeature(t *testing.T, repo, meta string) {
	t.Helper()
	tgz := featureTgzBytes(t, map[string]string{"devcontainer-feature.json": meta, "install.sh": "#!/bin/sh\n"})
	digest := sha256Digest(tgz)
	r.blobs[digest] = tgz
	annotations, err := json.Marshal(map[string]string{"dev.containers.metadata": meta})
	if err != nil {
		t.Fatal(err)
	}
	r.manifests[repo] = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.devcontainers","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},` +
		`"layers":[{"mediaType":"application/vnd.devcontainers.layer.v1+tar","digest":"` + digest + `","size":` + strconv.Itoa(len(tgz)) + `}],` +
		`"annotations":` + string(annotations) + `}`)
}

func parseLockfile(t *testing.T, data string) features.Lockfile {
	t.Helper()
	var lf features.Lockfile
	if err := json.Unmarshal([]byte(data), &lf); err != nil {
		t.Fatalf("not a lockfile: %v\n%s", err, data)
	}
	return lf
}

// upgrade --dry-run resolves the Features as build does (TS readFeaturesConfig)
// and prints their lockfile: each OCI Feature at the version its
// devcontainer-feature.json records, pinned to its manifest digest, the
// Features it depends on included, local Features left out.
func TestUpgradeDryRunPrintsLockfile(t *testing.T) {
	reg := newReadRegistry(t)
	h := reg.host
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.2.3","dependsOn":{"`+h+`/ns/dep:2":{}}}`)
	reg.addFeature(t, "ns/dep", `{"id":"dep","version":"2.0.1"}`)
	ws := writeOutdatedWorkspace(t, `{"`+h+`/ns/tool:1":{}, "./local":{}}`, "")
	writeFile(t, filepath.Join(ws, ".devcontainer", "local", "devcontainer-feature.json"), `{"id":"local","version":"0.1.0"}`, 0o644)
	got, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	digest := func(repo string) string {
		sum := sha256.Sum256(reg.manifests[repo])
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	want := map[string]features.LockfileEntry{
		h + "/ns/tool:1": {Version: "1.2.3", Resolved: h + "/ns/tool@" + digest("ns/tool"), Integrity: digest("ns/tool"), DependsOn: []string{h + "/ns/dep:2"}},
		h + "/ns/dep:2":  {Version: "2.0.1", Resolved: h + "/ns/dep@" + digest("ns/dep"), Integrity: digest("ns/dep")},
	}
	if lf := parseLockfile(t, got); !reflect.DeepEqual(lf.Features, want) {
		t.Errorf("lockfile features = %+v, want %+v", lf.Features, want)
	}
	if _, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote a lockfile: %v", err)
	}
}

// A Feature that cannot be resolved fails upgrade, as it fails TS
// generateFeaturesConfig, rather than being left out of the lockfile: nothing
// is printed or written.
func TestUpgradeFailsOnAnUnresolvableFeature(t *testing.T) {
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	missing := reg.host + "/ns/missing:1"
	want := "ERR: Feature '" + missing + "' could not be processed."
	for _, dryRun := range []bool{false, true} {
		ws := writeOutdatedWorkspace(t, `{"`+reg.host+`/ns/tool:1":{}, "`+missing+`":{}}`, "")
		args := []string{"--workspace-folder", ws}
		if dryRun {
			args = append(args, "--dry-run")
		}
		got, err := executeCmd(t, newUpgradeCmd(), args...)
		if err == nil || !strings.HasPrefix(err.Error(), want) || got != "" {
			t.Errorf("dry-run %t: err = %v, stdout = %q, want %q", dryRun, err, got, want)
		}
		if _, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")); !os.IsNotExist(err) {
			t.Errorf("dry-run %t: wrote a lockfile: %v", dryRun, err)
		}
	}
}

// Without --dry-run the lockfile is written next to the configuration
// --config names; one that cannot be written fails the command.
func TestUpgradeWritesLockfileNextToConfig(t *testing.T) {
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	cfg := filepath.Join(t.TempDir(), "custom.json")
	writeFile(t, cfg, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	ws := t.TempDir()
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--config", cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(features.LockfilePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if e := parseLockfile(t, string(data)).Features[reg.host+"/ns/tool:1"]; e.Version != "1.0.0" {
		t.Errorf("written entry = %+v, want version 1.0.0", e)
	}

	blocked := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, blocked, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	if err := os.Mkdir(features.LockfilePath(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--config", blocked); err == nil {
		t.Error("a lockfile path that is a directory: want an error")
	}
}

// upgrade truncates the lockfile before writing it (TS upgradeCommand.ts), so a
// lockfile with the same content in another layout is rewritten canonically,
// where build and up would leave it alone.
func TestUpgradeRewritesACosmeticallyDifferentLockfile(t *testing.T) {
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, cfg, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	ws := t.TempDir()
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--config", cfg); err != nil {
		t.Fatal(err)
	}
	lockPath := features.LockfilePath(cfg)
	canonical, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, canonical); err != nil {
		t.Fatal(err)
	}
	writeFile(t, lockPath, compact.String(), 0o644)
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, lockPath); got != string(canonical) {
		t.Errorf("lockfile = %q, want it rewritten as %q", got, canonical)
	}
}

// A lockfile upgrade can truncate but not read back fails the command, as TS
// writeLockfile rethrows readLocalFile's EACCES after the truncation.
func TestUpgradeFailsOnALockfileItCannotReadBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a write-only file")
	}
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, cfg, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	writeFile(t, features.LockfilePath(cfg), "{}", 0o200)
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", t.TempDir(), "--config", cfg); !errors.Is(err, os.ErrPermission) {
		t.Errorf("err = %v, want the lockfile's permission error", err)
	}
}

// upgrade reads the workspace given by --workspace-folder, and the current
// directory without it.
func TestUpgradeWorkspaceFolderDefaultsToCwd(t *testing.T) {
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	ws := writeOutdatedWorkspace(t, `{"`+reg.host+`/ns/tool:1":{}}`, "")
	t.Chdir(t.TempDir())
	if got, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run"); err != nil || len(parseLockfile(t, got).Features) != 1 {
		t.Errorf("--workspace-folder: err = %v, stdout = %q", err, got)
	}
	t.Chdir(ws)
	if got, err := executeCmd(t, newUpgradeCmd(), "--dry-run"); err != nil || len(parseLockfile(t, got).Features) != 1 {
		t.Errorf("cwd: err = %v, stdout = %q", err, got)
	}
}

// --feature and --target-version go together, and the target is a bare
// x / x.y / x.y.z version (TS upgradeCommand.ts .check()); a configuration that
// cannot be loaded, or a log file that cannot be opened, fails the command.
func TestUpgradeRejectsBadInvocations(t *testing.T) {
	ws := writeOutdatedWorkspace(t, `{}`, "")
	together := "The '--target-version' and '--feature' flag must be used together."
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--feature", "ghcr.io/o/f"}, together},
		{[]string{"--target-version", "2"}, together},
		{[]string{"--feature", "ghcr.io/o/f", "--target-version", "2.x"}, "Invalid version '2.x'.  Must be in the form of 'x', 'x.y', or 'x.y.z'"},
		{[]string{"--log-file", t.TempDir()}, "open log file"},
		{[]string{"--workspace-folder", t.TempDir()}, "devcontainer.json"},
	}
	for _, c := range cases {
		args := append([]string{"--workspace-folder", ws, "--dry-run"}, c.args...)
		if _, err := executeCmd(t, newUpgradeCmd(), args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.args, err, c.want)
		}
	}
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", "ghcr.io/o/f", "--target-version", "2.1"); err == nil || err.Error() != "Failed to update lockfile" {
		t.Errorf("--feature with a bare --target-version: err = %v, want it past the flag checks", err)
	}
}

// A configuration without Features has no Features config to lock, which TS
// upgrade fails on ("Failed to update lockfile") rather than writing an empty
// lockfile, with or without --dry-run.
func TestUpgradeFailsWithoutFeatures(t *testing.T) {
	for _, feats := range []string{`{}`, `null`} {
		for _, dryRun := range []bool{false, true} {
			ws := writeOutdatedWorkspace(t, feats, "")
			args := []string{"--workspace-folder", ws}
			if dryRun {
				args = append(args, "--dry-run")
			}
			got, err := executeCmd(t, newUpgradeCmd(), args...)
			if _, ok := coreerrors.AsContainerError(err); !ok || err.Error() != "Failed to update lockfile" || got != "" {
				t.Errorf("features %s, dry-run %t: err = %v, stdout = %q", feats, dryRun, err, got)
			}
			if _, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")); !os.IsNotExist(err) {
				t.Errorf("features %s, dry-run %t: wrote a lockfile: %v", feats, dryRun, err)
			}
		}
	}
}

// The keys of a "features" value that is not an object are not Feature ids.
func TestFeatureKeyOrderFromFileNonObjectFeatures(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, cfg, `{"features":["a","b"]}`, 0o644)
	if keys, err := featureKeyOrderFromFile(cfg); err != nil || keys != nil {
		t.Errorf("keys = %q, err = %v, want none", keys, err)
	}
}

// A column is as wide as its widest cell, an empty column has no width, and a
// cell one shorter than its column is padded by one.
func TestTextTableWidths(t *testing.T) {
	got := textTable([][]string{{"", "ab", "x"}, {"", "a", "y"}})
	if want := "  ab  x\n  a   y"; got != want {
		t.Errorf("table = %q, want %q", got, want)
	}
}

// The tag or digest is cut at the first ':' or '@' after the last '/', even
// right after it (TS /[:@][^/]*$/).
func TestGetFeatureIdWithoutVersionRightAfterSlash(t *testing.T) {
	for in, want := range map[string]string{"o/@sha256:x": "o/", "@sha256:x": "", "o/:1": "o/"} {
		if got := getFeatureIdWithoutVersion(in); got != want {
			t.Errorf("getFeatureIdWithoutVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// --log-level is one of TS upgrade's choices, error among them, and yargs
// rejects any other before .check() looks at --feature/--target-version.
func TestUpgradeLogLevelChoices(t *testing.T) {
	ws := writeOutdatedWorkspace(t, `{}`, "")
	want := `Invalid value "warn" for --log-level. Choose from: error, info, debug, trace`
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--log-level", "warn", "--feature", "ghcr.io/o/f"); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--log-level", "error", "--feature", "ghcr.io/o/f"); err == nil || !strings.Contains(err.Error(), "must be used together") {
		t.Errorf("--log-level error: err = %v, want the --feature/--target-version check", err)
	}
}

// A --config file that does not exist is TS getConfig's "not found" error,
// naming the resolved path.
func TestUpgradeMissingConfigIsNotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	want := "Dev container config (" + missing + ") not found."
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", t.TempDir(), "--config", missing); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// upgrade logs TS's header once the configuration loads, none when it does
// not, and nothing of its own after writing the lockfile.
func TestUpgradeLogsAsTheReference(t *testing.T) {
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, cfg, `{"image":"alpine","features":{"`+reg.host+`/ns/tool:1":{}}}`, 0o644)
	logFile := filepath.Join(t.TempDir(), "upgrade.log")
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", t.TempDir(), "--config", cfg, "--log-file", logFile); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, logFile)), "\n")
	if !strings.Contains(lines[0], "@devcontainers/cli ") || strings.Contains(readFile(t, logFile), "Lockfile updated.") {
		t.Errorf("log = %q, want the header first and no \"Lockfile updated.\"", lines)
	}

	missing := filepath.Join(t.TempDir(), "nope.json")
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", t.TempDir(), "--config", missing, "--log-file", logFile); err == nil {
		t.Fatal("a missing --config: want an error")
	}
	if got := readFile(t, logFile); got != "" {
		t.Errorf("log of a configuration that does not load = %q, want none", got)
	}
}

// --feature/--target-version pin the Feature in devcontainer.json before the
// lockfile is generated, as TS updateFeatureVersionInConfig does, also with
// --dry-run: the first key, in file order, naming the Feature whatever its
// version is replaced, as a pattern, everywhere in the file's text.
func TestUpgradePinsTheFeatureVersionInConfig(t *testing.T) {
	reg := newReadRegistry(t)
	h := reg.host
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"2.0.0"}`)
	reg.addFeature(t, "ns/other", `{"id":"other","version":"1.0.0"}`)
	ws := t.TempDir()
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	writeFile(t, cfg, "{\n\t// pinned: "+h+"/ns/tool:1\n\t\"image\": \"alpine\",\n\t\"features\": {\n\t\t\""+h+"/ns/other:1\": {},\n\t\t\""+h+"/ns/tool:1\": {}\n\t}\n}\n", 0o644)
	logFile := filepath.Join(t.TempDir(), "upgrade.log")
	got, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", h+"/ns/tool:1.5", "--target-version", "2", "--log-file", logFile)
	if err != nil {
		t.Fatal(err)
	}
	wantCfg := "{\n\t// pinned: " + h + "/ns/tool:2\n\t\"image\": \"alpine\",\n\t\"features\": {\n\t\t\"" + h + "/ns/other:1\": {},\n\t\t\"" + h + "/ns/tool:2\": {}\n\t}\n}\n"
	if got := readFile(t, cfg); got != wantCfg {
		t.Errorf("config = %q, want %q", got, wantCfg)
	}
	if e, ok := parseLockfile(t, got).Features[h+"/ns/tool:2"]; !ok || e.Version != "2.0.0" {
		t.Errorf("lockfile = %s, want it generated from the pinned configuration", got)
	}
	log := readFile(t, logFile)
	for _, want := range []string{
		"Updating '" + h + "/ns/tool:1.5' to '2' in devcontainer.json",
		"Updating config file: '" + cfg + "'",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log = %q, want it to hold %q", log, want)
		}
	}
	if strings.Contains(log, "\"image\": \"alpine\"") {
		t.Errorf("log = %q, want the updated text only at trace level", log)
	}
}

// Of two keys naming the Feature only the first in file order is replaced;
// the key is read as a regular expression, so a '.' in it matches any
// character, and a key that is not a valid one fails the command.
func TestUpgradeFeatureKeyIsAPattern(t *testing.T) {
	reg := newReadRegistry(t)
	h := reg.host
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"2.0.0"}`)
	ws := t.TempDir()
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	writeFile(t, cfg, `{"image":"alpine","x":"`+h+`/ns/tool:1X0","features":{"`+h+`/ns/tool:1.0":{},"`+h+`/ns/tool:3":{}}}`, 0o644)
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", h+"/ns/tool", "--target-version", "2"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg), `{"image":"alpine","x":"`+h+`/ns/tool:2","features":{"`+h+`/ns/tool:2":{},"`+h+`/ns/tool:3":{}}}`; got != want {
		t.Errorf("config = %q, want %q", got, want)
	}

	bad := `{"image":"alpine","features":{"` + h + `/ns/to(ol:1":{}}}`
	writeFile(t, cfg, bad, 0o644)
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", h+"/ns/to(ol", "--target-version", "2"); err == nil || !strings.Contains(err.Error(), "missing closing )") {
		t.Errorf("an invalid pattern: err = %v", err)
	}
	if got := readFile(t, cfg); got != bad {
		t.Errorf("config = %q, want it untouched", got)
	}
}

// A configuration with no Feature to pin is left as it is: without Features
// TS logs so at debug level, and with Features none of which is the one
// asked for, the unchanged text at trace level.
func TestUpgradeLeavesAConfigWithoutTheFeature(t *testing.T) {
	reg := newReadRegistry(t)
	h := reg.host
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	cases := []struct {
		features, level, wantLog string
		wantErr                  bool
	}{
		{`null`, "debug", "No Features found in '", true},
		{`{"` + h + `/ns/tool:1":{}}`, "trace", "No changes to config file: ", false},
	}
	for _, c := range cases {
		ws := writeOutdatedWorkspace(t, c.features, "")
		cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
		before := readFile(t, cfg)
		logFile := filepath.Join(t.TempDir(), "upgrade.log")
		_, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", h+"/ns/other", "--target-version", "2", "--log-level", c.level, "--log-file", logFile)
		if (err != nil) != c.wantErr {
			t.Errorf("features %s: err = %v, want error %t", c.features, err, c.wantErr)
		}
		if got := readFile(t, cfg); got != before {
			t.Errorf("features %s: config = %q, want it untouched", c.features, got)
		}
		if log := readFile(t, logFile); !strings.Contains(log, c.wantLog+cfg) || strings.Contains(log, "Updating config file") {
			t.Errorf("features %s: log = %q, want %q", c.features, log, c.wantLog+cfg)
		}
	}
}

// A local Feature outside the .devcontainer folder fails upgrade as TS
// processFeatureIdentifier fails it, with or without --dry-run, rather than
// being written to the lockfile.
func TestUpgradeRefusesALocalFeatureOutsideDevcontainer(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		ws := writeOutdatedWorkspace(t, `{"../f":{}}`, "")
		writeFile(t, filepath.Join(ws, "f", "devcontainer-feature.json"), `{"id":"f","version":"1.0.0"}`, 0o644)
		args := []string{"--workspace-folder", ws}
		if dryRun {
			args = append(args, "--dry-run")
		}
		got, err := executeCmd(t, newUpgradeCmd(), args...)
		if err == nil || !strings.HasPrefix(err.Error(), "ERR: Feature '../f' could not be processed.") || got != "" {
			t.Errorf("dry-run %t: err = %v, stdout = %q", dryRun, err, got)
		}
		if _, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")); !os.IsNotExist(err) {
			t.Errorf("dry-run %t: wrote a lockfile: %v", dryRun, err)
		}
	}
}

// A lockfile upgrade creates gets Node's default mode, 0666 minus the umask,
// as TS writeLocalFile(lockfilePath, "") gives it before the lockfile is
// written.
func TestUpgradeCreatesTheLockfileWithNodesMode(t *testing.T) {
	old := syscall.Umask(0o002)
	t.Cleanup(func() { syscall.Umask(old) })
	reg := newReadRegistry(t)
	reg.addFeature(t, "ns/tool", `{"id":"tool","version":"1.0.0"}`)
	ws := writeOutdatedWorkspace(t, `{"`+reg.host+`/ns/tool:1":{}}`, "")
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Errorf("lockfile mode = %v, want 0664", got)
	}
}
