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
	"strings"
	"testing"

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
	if got, err := executeCmd(t, newOutdatedCmd(), "--workspace-folder", ws); err != nil || got != "No features configured.\n" {
		t.Errorf("--workspace-folder: err = %v, stdout = %q", err, got)
	}
	t.Chdir(ws)
	if got, err := executeCmd(t, newOutdatedCmd()); err != nil || got != "No features configured.\n" {
		t.Errorf("cwd: err = %v, stdout = %q", err, got)
	}
}

// manifestWithAnnotations is a Feature manifest carrying the given annotations
// object (JSON), or none when it is empty.
func manifestWithAnnotations(annotations string) []byte {
	m := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.devcontainers","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},` +
		`"layers":[{"mediaType":"application/vnd.devcontainers.layer.v1+tar","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2}]`
	if annotations != "" {
		m += `,"annotations":` + annotations
	}
	return []byte(m + `}`)
}

// upgradeRegistry serves Features whose lockfile version comes from each of
// the places upgrade looks: the manifest's metadata, else the newest published
// version the tag wants, else the tag itself.
func upgradeRegistry(t *testing.T) (*readRegistry, string) {
	t.Helper()
	reg := newReadRegistry(t)
	reg.manifests["ns/annotated"] = featureManifest("annotated")
	reg.tags["ns/annotated"] = []string{"1.0.0", "1.5.0"}
	reg.manifests["ns/plain"] = manifestWithAnnotations("")
	reg.tags["ns/plain"] = []string{"1.0.0", "1.5.0", "2.0.0"}
	reg.manifests["ns/noversion"] = manifestWithAnnotations(`{"dev.containers.metadata":"{\"id\":\"noversion\"}"}`)
	reg.tags["ns/noversion"] = []string{"1.2.0"}
	reg.manifests["ns/untagged"] = manifestWithAnnotations("")
	h := reg.host
	features := `{"` + h + `/ns/annotated:1":{}, "` + h + `/ns/plain:1":{}, "` + h + `/ns/noversion:1":{}, "` +
		h + `/ns/untagged:1":{}, "` + h + `/ns/missing:1":{}, "./local":{}}`
	return reg, features
}

func parseLockfile(t *testing.T, data string) features.Lockfile {
	t.Helper()
	var lf features.Lockfile
	if err := json.Unmarshal([]byte(data), &lf); err != nil {
		t.Fatalf("not a lockfile: %v\n%s", err, data)
	}
	return lf
}

// upgrade --dry-run prints the lockfile for every OCI Feature whose manifest
// resolves, pinned to its manifest digest, at the version TS records; local
// Features and ones without a manifest are left out.
func TestUpgradeDryRunPrintsLockfile(t *testing.T) {
	reg, feats := upgradeRegistry(t)
	ws := writeOutdatedWorkspace(t, feats, "")
	got, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	lf := parseLockfile(t, got)
	h := reg.host
	want := map[string]string{
		h + "/ns/annotated:1": "1.0.0",
		h + "/ns/plain:1":     "1.5.0",
		h + "/ns/noversion:1": "1.2.0",
		h + "/ns/untagged:1":  "1",
	}
	if len(lf.Features) != len(want) {
		t.Errorf("lockfile features = %v, want %v", lf.Features, want)
	}
	for id, version := range want {
		if e := lf.Features[id]; e.Version != version {
			t.Errorf("%s version = %q, want %q", id, e.Version, version)
		}
	}
	sum := sha256.Sum256(reg.manifests["ns/annotated"])
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if e := lf.Features[h+"/ns/annotated:1"]; e.Integrity != digest || e.Resolved != h+"/ns/annotated@"+digest {
		t.Errorf("annotated entry = %+v, want it pinned to %s", e, digest)
	}
	if _, err := os.Stat(filepath.Join(ws, ".devcontainer", "devcontainer-lock.json")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote a lockfile: %v", err)
	}
}

// Without --dry-run the lockfile is written next to the configuration
// --config names; one that cannot be written fails the command.
func TestUpgradeWritesLockfileNextToConfig(t *testing.T) {
	reg := newReadRegistry(t)
	reg.manifests["ns/tool"] = featureManifest("tool")
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
	reg.manifests["ns/tool"] = featureManifest("tool")
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
	reg.manifests["ns/tool"] = featureManifest("tool")
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
	ws := writeOutdatedWorkspace(t, `{}`, "")
	t.Chdir(t.TempDir())
	if got, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run"); err != nil || parseLockfile(t, got).Features == nil {
		t.Errorf("--workspace-folder: err = %v, stdout = %q", err, got)
	}
	t.Chdir(ws)
	if got, err := executeCmd(t, newUpgradeCmd(), "--dry-run"); err != nil || parseLockfile(t, got).Features == nil {
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
	if _, err := executeCmd(t, newUpgradeCmd(), "--workspace-folder", ws, "--dry-run", "--feature", "ghcr.io/o/f", "--target-version", "2.1"); err != nil {
		t.Errorf("--feature with a bare --target-version: %v", err)
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
