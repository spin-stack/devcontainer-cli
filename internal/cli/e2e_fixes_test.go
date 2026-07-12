package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeFixture materializes a self-contained workspace from a path->content map
// under a temp dir and returns the workspace root.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// dockerExec runs `docker exec <id> sh -lc <cmd>` and returns trimmed combined output.
func dockerExec(t *testing.T, id, cmd string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", id, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %q failed: %v\n%s", cmd, err, out)
	}
	return strings.TrimSpace(string(out))
}

func removeByID(id string) {
	if id != "" {
		exec.Command("docker", "rm", "-f", id).Run()
	}
}

// TestE2E_FeatureUserHomeAndLocalEnv validates two upstream fixes on a real
// build+up with a local Feature:
//   - #331: the Feature install script sees a non-empty _REMOTE_USER_HOME.
//   - #308: a Feature's containerEnv ${localEnv:VAR} is resolved (not literal).
func TestE2E_FeatureUserHomeAndLocalEnv(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	t.Setenv("E2E_LOCALENV_MARKER", "resolved-localenv-1234")

	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{
			"image": "mcr.microsoft.com/devcontainers/base:ubuntu",
			"remoteUser": "vscode",
			"features": { "./localfeat": {} }
		}`,
		".devcontainer/localfeat/devcontainer-feature.json": `{
			"id": "localfeat",
			"version": "1.0.0",
			"name": "localfeat",
			"containerEnv": { "MY_LOCAL_ENV": "${localEnv:E2E_LOCALENV_MARKER}" }
		}`,
		".devcontainer/localfeat/install.sh": "#!/bin/sh\nset -e\n" +
			`echo "REMOTE_USER_HOME=[$_REMOTE_USER_HOME]" > /e2e-feature-marker` + "\n",
	})

	upOut := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	id, _ := parseJSON(t, upOut)["containerId"].(string)
	if id == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(id)

	// #331: install.sh captured the remote user's home at build time.
	if got := dockerExec(t, id, "cat /e2e-feature-marker"); !strings.Contains(got, "REMOTE_USER_HOME=[/home/vscode]") {
		t.Errorf("#331 _REMOTE_USER_HOME not resolved in Feature build: %q", got)
	}
	// #308: the Feature's containerEnv ${localEnv:...} was substituted.
	if got := dockerExec(t, id, "printenv MY_LOCAL_ENV"); got != "resolved-localenv-1234" {
		t.Errorf("#308 Feature containerEnv ${localEnv} = %q, want resolved-localenv-1234", got)
	}
}

// TestE2E_RemoveExistingBeforeInitialize validates #844: with
// --remove-existing-container, the old container is removed BEFORE
// initializeCommand, so a failing initializeCommand does not leave it running.
func TestE2E_RemoveExistingBeforeInitialize(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu","overrideCommand":true}`,
	})

	// 1) Provision the container.
	upOut := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	oldID, _ := parseJSON(t, upOut)["containerId"].(string)
	if oldID == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(oldID)

	// 2) Add a failing initializeCommand, then up --remove-existing-container.
	//    The up must fail (init exits 1) — but the old container must be gone.
	if err := os.WriteFile(filepath.Join(ws, ".devcontainer", "devcontainer.json"),
		[]byte(`{"image":"mcr.microsoft.com/devcontainers/base:ubuntu","overrideCommand":true,"initializeCommand":"exit 1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	upOut2 := runCLI(t, "up", "--workspace-folder", ws, "--remove-existing-container", "--skip-post-create")
	if oc := parseJSON(t, upOut2)["outcome"]; oc != "error" {
		t.Fatalf("second up outcome = %v, want error (initializeCommand fails)", oc)
	}

	// The old container must have been removed before initializeCommand ran.
	if exec.Command("docker", "inspect", oldID).Run() == nil {
		t.Errorf("#844 old container %s still exists after a failed rebuild; removal did not precede initializeCommand", oldID[:12])
	}
}

// TestE2E_ComposeBuildLabel validates #930: `build --label` on a Compose config
// applies the label even without --image-name.
func TestE2E_ComposeBuildLabel(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ws := writeFixture(t, map[string]string{
		"docker-compose.yml": "services:\n  app:\n    build:\n      context: .\n      dockerfile: Dockerfile\n    command: sleep infinity\n",
		"Dockerfile":         "FROM mcr.microsoft.com/devcontainers/base:ubuntu\n",
		".devcontainer/devcontainer.json": `{
			"dockerComposeFile": "../docker-compose.yml",
			"service": "app",
			"workspaceFolder": "/workspace"
		}`,
	})

	buildOut := runCLI(t, "build", "--workspace-folder", ws, "--label", "e2e.compose.label=applied")
	assertOutcome(t, buildOut, "success")
	names, _ := parseJSON(t, buildOut)["imageName"].([]interface{})
	if len(names) == 0 {
		t.Fatal("no imageName in build output")
	}
	img, _ := names[0].(string)
	defer exec.Command("docker", "rmi", "-f", img).Run()

	out, err := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "e2e.compose.label"}}`, img).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s failed: %v\n%s", img, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "applied" {
		t.Errorf("#930 image %s label e2e.compose.label = %q, want applied (dropped without --image-name)", img, got)
	}
}

// TestE2E_SiblingDockerignore validates #969: when the Dockerfile is renamed to
// inject a final stage name, the user's sibling <Dockerfile>.dockerignore still
// applies, so excluded files do not leak into the build context.
func TestE2E_SiblingDockerignore(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	// The Dockerfile has an UNNAMED final stage, so the CLI rewrites it to a temp
	// file (the path that used to drop the sibling .dockerignore).
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json":       `{"build":{"dockerfile":"Dockerfile"}}`,
		".devcontainer/Dockerfile":              "FROM mcr.microsoft.com/devcontainers/base:ubuntu\nCOPY . /ctx-check/\nRUN if [ -e /ctx-check/secret.txt ]; then echo LEAKED > /dockerignore-result; else echo IGNORED_OK > /dockerignore-result; fi\n",
		".devcontainer/Dockerfile.dockerignore": "secret.txt\n",
		".devcontainer/secret.txt":              "SUPERSECRET\n",
	})

	buildOut := runCLI(t, "build", "--workspace-folder", ws, "--image-name", "e2e-dockerignore:test")
	assertOutcome(t, buildOut, "success")
	defer exec.Command("docker", "rmi", "-f", "e2e-dockerignore:test").Run()

	out, err := exec.Command("docker", "run", "--rm", "e2e-dockerignore:test", "cat", "/dockerignore-result").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "IGNORED_OK" {
		t.Errorf("#969 sibling .dockerignore not applied on rename: got %q (secret.txt leaked into the context)", got)
	}
}

// TestE2E_ReadonlyMountObject validates #881: a top-level mount OBJECT with
// readonly:true produces a genuinely read-only bind mount in the container.
func TestE2E_ReadonlyMountObject(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	src := writeFixture(t, map[string]string{"file.txt": "hello\n"})
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{
			"image": "mcr.microsoft.com/devcontainers/base:ubuntu",
			"overrideCommand": true,
			"mounts": [
				{ "type": "bind", "source": "` + src + `", "target": "/ro-mount", "readonly": true }
			]
		}`,
	})

	upOut := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	id, _ := parseJSON(t, upOut)["containerId"].(string)
	if id == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(id)

	// Writing into the mount must fail because it is read-only.
	out, err := exec.Command("docker", "exec", id, "sh", "-c", "echo x > /ro-mount/probe 2>&1; echo rc=$?").CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); !strings.Contains(got, "Read-only file system") || strings.Contains(got, "rc=0") {
		t.Errorf("#881 mount object readonly:true did not produce a read-only mount: %q", got)
	}
}

// TestE2E_UpdateUIDSystemUser validates #109: updateRemoteUserUID's chown no
// longer aborts the remap build when the remote user has no home dir on disk
// (system user). Pre-fix the chown failed, the wrapper swallowed it, and the
// UID/GID remap silently did NOT happen; post-fix the remap succeeds. We assert
// the remap actually took effect (svcuser's uid == the host uid).
func TestE2E_UpdateUIDSystemUser(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	if os.Getuid() == 0 {
		t.Skip("updateRemoteUserUID is a no-op when the host runs as root")
	}
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{"build":{"dockerfile":"Dockerfile"},"remoteUser":"svcuser","updateRemoteUserUID":true,"overrideCommand":true}`,
		// A system user (uid 999) with a home path in /etc/passwd that does NOT
		// exist on disk — the case where the pre-fix chown -R aborted the build.
		// The base MUST NOT already own uid/gid 1000 (the host uid/gid), or the
		// remap short-circuits on "user with UID exists" and never reaches the
		// chown. Plain ubuntu:22.04 has neither, so the remap proceeds.
		".devcontainer/Dockerfile": "FROM ubuntu:22.04\n" +
			"RUN useradd --system --no-create-home --home-dir /home/svcuser --shell /bin/sh svcuser\n",
	})

	upOut := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	id, _ := parseJSON(t, upOut)["containerId"].(string)
	if id == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(id)

	wantUID := strconv.Itoa(os.Getuid())
	if got := dockerExec(t, id, "getent passwd svcuser | cut -d: -f3"); got != wantUID {
		t.Errorf("#109 svcuser uid = %q, want %q — the UID/GID remap silently no-op'd (chown aborted the build)", got, wantUID)
	}
}
