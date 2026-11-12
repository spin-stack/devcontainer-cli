package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

var differentialRuntimeKnown = map[string]map[string]string{
	"up": {
		"dockerfile-with-syntax":           "legacy GitHub Release Feature (accepted limitation)",
		"dockerfile-with-target":           "legacy GitHub Release Feature (accepted limitation)",
		"compose-Dockerfile-with-features": "legacy GitHub Release Feature (accepted limitation)",
		"compose-Dockerfile-with-target":   "legacy GitHub Release Feature (accepted limitation)",
		"compose-image-with-features":      "legacy GitHub Release Feature (accepted limitation)",
	},
	"build": {
		"dockerfile-with-syntax":           "legacy GitHub Release Feature (accepted limitation)",
		"dockerfile-with-target":           "legacy GitHub Release Feature (accepted limitation)",
		"compose-Dockerfile-with-features": "legacy GitHub Release Feature (accepted limitation)",
		"compose-Dockerfile-with-target":   "legacy GitHub Release Feature (accepted limitation)",
		"compose-image-with-features":      "legacy GitHub Release Feature (accepted limitation)",
	},
}

func TestDifferentialUp(t *testing.T) {
	runDifferentialRuntime(t, "up", differentialUpSnapshot)
}

func TestDifferentialBuild(t *testing.T) {
	runDifferentialRuntime(t, "build", differentialBuildSnapshot)
}

type differentialSnapshot func(t *testing.T, cli []string, ws string) map[string]any

func runDifferentialRuntime(t *testing.T, command string, snapshot differentialSnapshot) {
	if os.Getenv("PARITY_DIFFERENTIAL") == "" {
		t.Skip("set PARITY_DIFFERENTIAL=1 (task parity:differential): needs the oracle, Docker and the network")
	}
	repoRoot := findRepoRoot(t)
	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Fatalf("TS reference oracle not compiled (task reference): %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Fatalf("Docker is required: %v", err)
	}
	ts := []string{"node", filepath.Join(repoRoot, "reference", "devcontainer.js")}
	goCLI := os.Getenv("PARITY_CLI_GO")
	if goCLI == "" {
		goCLI = filepath.Join(repoRoot, "devcontainer")
	}

	configs := filepath.Join(repoRoot, "reference", "src", "test", "configs")
	fixtures := differentialFixtures(t, configs)
	known := differentialRuntimeKnown[command]
	for name := range known {
		if !slices.Contains(fixtures, name) {
			t.Errorf("differentialRuntimeKnown[%q] lists %q, which is no fixture", command, name)
		}
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(name, "arm64") && os.Getenv("PARITY_ARM64") != "true" {
				t.Skip("arm64 fixture (set PARITY_ARM64=true; needs arm64 emulation)")
			}
			src := filepath.Join(configs, name)
			tsOut := snapshot(t, ts, differentialWorkspace(t, src, "ts"))
			goOut := snapshot(t, []string{goCLI}, differentialWorkspace(t, src, "go"))
			same := reflect.DeepEqual(tsOut, goOut)
			reason, isKnown := known[name]
			switch {
			case same && isKnown:
				t.Errorf("%s %s no longer differs (%s): remove it from differentialRuntimeKnown", command, name, reason)
			case !same && isKnown:
				t.Skipf("known difference: %s", reason)
			case !same:
				t.Errorf("%s differs\n%s", command, differentialDiff(tsOut, goOut))
			}
		})
	}
}

func differentialWorkspace(t *testing.T, fixture, side string) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), side, "ws")
	if err := copyDir(fixture, ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func differentialUpSnapshot(t *testing.T, cli []string, ws string) map[string]any {
	t.Helper()
	defer differentialCleanup(t, ws)
	code, result := runDifferentialCLI(t, cli, "up", "--workspace-folder", ws)
	snap := map[string]any{"exitCode": code, "result": normalizeDifferential(result, ws)}
	containerID, _ := asMap(result)["containerId"].(string)
	if code != 0 || containerID == "" {
		return snap
	}
	container := dockerInspect(t, "container", containerID)
	snap["container"] = normalizeDifferential(containerView(container), ws)
	if image, _ := container["Image"].(string); image != "" {
		snap["image"] = normalizeDifferential(imageView(dockerInspect(t, "image", image)), ws)
	}
	if user, _ := asMap(result)["remoteUser"].(string); user != "" {
		out, err := exec.CommandContext(t.Context(), "docker", "exec", "-u", user, containerID, "id").CombinedOutput()
		snap["remoteUserId"] = strings.TrimSpace(string(out))
		if err != nil {
			snap["remoteUserIdError"] = err.Error()
		}
	}
	return snap
}

const differentialImageName = "devcontainer-differential:latest"

func differentialBuildSnapshot(t *testing.T, cli []string, ws string) map[string]any {
	t.Helper()
	defer differentialCleanup(t, ws)
	defer exec.Command("docker", "rmi", "-f", differentialImageName).Run()
	code, result := runDifferentialCLI(t, cli, "build", "--workspace-folder", ws, "--image-name", differentialImageName)
	snap := map[string]any{"exitCode": code, "result": normalizeDifferential(result, ws)}
	if code == 0 {
		snap["image"] = normalizeDifferential(imageView(dockerInspect(t, "image", differentialImageName)), ws)
	}
	return snap
}

func runDifferentialCLI(t *testing.T, cli []string, args ...string) (int, any) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), cli[0], append(cli[1:], args...)...)
	cmd.Dir = findRepoRoot(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", cli, err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	last := lines[len(lines)-1]
	var out any
	if err := json.Unmarshal([]byte(last), &out); err != nil {
		out = last
	}
	if code != 0 && testing.Verbose() {
		t.Logf("%s %s: exit %d\n%s", filepath.Base(cli[len(cli)-1]), args[0], code, tail(stderr.String(), 2000))
	}
	return code, out
}

func differentialCleanup(t *testing.T, ws string) {
	t.Helper()
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=devcontainer.local_folder="+ws).Output()
	ids := strings.Fields(string(out))
	images := map[string]bool{}
	projects := map[string]bool{}
	for _, id := range ids {
		c := dockerInspect(t, "container", id)
		if img, _ := c["Image"].(string); img != "" {
			images[img] = true
		}
		labels := asMap(asMap(c["Config"])["Labels"])
		if p, _ := labels["com.docker.compose.project"].(string); p != "" {
			projects[p] = true
		}
	}
	if len(ids) > 0 {
		exec.Command("docker", append([]string{"rm", "-f", "-v"}, ids...)...).Run()
	}
	for p := range projects {
		exec.Command("docker", "compose", "-p", p, "down", "-v", "--remove-orphans").Run()
		exec.Command("sh", "-c", "docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^"+regexp.QuoteMeta(p)+"[-_]' | xargs -r docker rmi -f").Run()
	}
	for img := range images {
		exec.Command("docker", "rmi", "-f", img).Run()
	}
	exec.Command("sh", "-c", "docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^vsc-ws-' | xargs -r docker rmi -f").Run()
}

func dockerInspect(t *testing.T, kind, id string) map[string]any {
	t.Helper()
	out, err := exec.Command("docker", kind, "inspect", id).Output()
	if err != nil {
		t.Fatalf("docker %s inspect %s: %v", kind, id, err)
	}
	var v []map[string]any
	if err := json.Unmarshal(out, &v); err != nil || len(v) != 1 {
		t.Fatalf("docker %s inspect %s: %v", kind, id, err)
	}
	return v[0]
}

func containerView(c map[string]any) map[string]any {
	cfg, host := asMap(c["Config"]), asMap(c["HostConfig"])
	var mounts []any
	for _, m := range asSlice(c["Mounts"]) {
		mm := asMap(m)
		source := mm["Source"]
		if mm["Type"] == "volume" {
			source = mm["Name"]
		}
		mounts = append(mounts, map[string]any{"type": mm["Type"], "source": source, "destination": mm["Destination"], "rw": mm["RW"]})
	}
	sortByJSON(mounts)
	return map[string]any{
		"user":        cfg["User"],
		"env":         sortedStrings(cfg["Env"]),
		"entrypoint":  cfg["Entrypoint"],
		"cmd":         cfg["Cmd"],
		"workingDir":  cfg["WorkingDir"],
		"labels":      viewLabels(asMap(cfg["Labels"])),
		"mounts":      mounts,
		"init":        host["Init"],
		"privileged":  host["Privileged"],
		"capAdd":      sortedStrings(host["CapAdd"]),
		"securityOpt": sortedStrings(host["SecurityOpt"]),
	}
}

func imageView(img map[string]any) map[string]any {
	cfg := asMap(img["Config"])
	return map[string]any{
		"user":       cfg["User"],
		"env":        sortedStrings(cfg["Env"]),
		"entrypoint": cfg["Entrypoint"],
		"cmd":        cfg["Cmd"],
		"labels":     viewLabels(asMap(cfg["Labels"])),
	}
}

func viewLabels(labels map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range labels {
		switch k {
		case "com.docker.compose.config-hash", "com.docker.compose.version", "com.docker.compose.container-number",
			"com.docker.compose.oneoff", "com.docker.compose.depends_on", "com.docker.compose.image",
			"com.docker.compose.replace", "desktop.docker.io/binds/0/Source", "desktop.docker.io/binds/0/SourceKind":
			continue
		}
		if s, ok := v.(string); ok && k == "devcontainer.metadata" {
			var parsed any
			if json.Unmarshal([]byte(s), &parsed) == nil {
				v = parsed
			}
		}
		out[k] = v
	}
	return out
}

var (
	differentialHashRe           = regexp.MustCompile(`(vsc-ws-)[0-9a-f]{64}`)
	differentialDigest           = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	differentialHexIDRe          = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	differentialOverrideRe       = regexp.MustCompile(`(docker-compose\.devcontainer\.\w+)-\d+(?:-[0-9a-f-]{36})?`)
	differentialDevcontainerIDRe = regexp.MustCompile(`(dind-var-lib-docker-)[0-9a-v]{52}`)
)

func normalizeDifferential(v any, ws string) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	s := strings.ReplaceAll(string(data), ws, "<WS>")
	s = strings.ReplaceAll(s, filepath.Dir(ws), "<WS>/..")
	s = differentialHashRe.ReplaceAllString(s, "${1}<HASH>")
	s = featuresFolderRe.ReplaceAllString(s, "${1}<VERSION>-<MS>")
	s = differentialDigest.ReplaceAllString(s, "sha256:<DIGEST>")
	s = differentialHexIDRe.ReplaceAllString(s, "<ID>")
	s = differentialOverrideRe.ReplaceAllString(s, "${1}-<MS>")
	s = differentialDevcontainerIDRe.ReplaceAllString(s, "${1}<DEVCONTAINER_ID>")
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return s
	}
	return out
}

func differentialDiff(ts, goOut map[string]any) string {
	var b strings.Builder
	keys := map[string]bool{}
	for k := range ts {
		keys[k] = true
	}
	for k := range goOut {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if !reflect.DeepEqual(ts[k], goOut[k]) {
			b.WriteString("--- " + k + " (TS)\n" + pretty(ts[k]) + "\n--- " + k + " (Go)\n" + pretty(goOut[k]) + "\n")
		}
	}
	return b.String()
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func sortedStrings(v any) []string {
	var out []string
	for _, e := range asSlice(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sortByJSON(items []any) {
	sort.Slice(items, func(i, j int) bool {
		a, _ := json.Marshal(items[i])
		b, _ := json.Marshal(items[j])
		return string(a) < string(b)
	})
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
