package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := uniqueWorkspace(t)
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

// Concurrent runs share the Docker daemon, and a Compose project's name and
// images come from the workspace's basename.
func uniqueWorkspace(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(t.TempDir(), "ws-")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

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

func TestE2E_FeatureUserHomeAndLocalEnv(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	t.Setenv("E2E_LOCALENV_MARKER", "resolved-localenv-1234")
	mountSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(mountSrc, "marker"), []byte("mounted-from-localenv"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("E2E_LOCALENV_MOUNT", mountSrc)

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
			"containerEnv": { "MY_LOCAL_ENV": "${localEnv:E2E_LOCALENV_MARKER}" },
			"mounts": [{ "source": "${localEnv:E2E_LOCALENV_MOUNT}", "target": "/e2e-localenv-mount", "type": "bind" }]
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

	if got := dockerExec(t, id, "cat /e2e-feature-marker"); !strings.Contains(got, "REMOTE_USER_HOME=[/home/vscode]") {
		t.Errorf("#331 _REMOTE_USER_HOME not resolved in Feature build: %q", got)
	}
	if got := dockerExec(t, id, "printenv MY_LOCAL_ENV"); got != "resolved-localenv-1234" {
		t.Errorf("#308 Feature containerEnv ${localEnv} = %q, want resolved-localenv-1234", got)
	}
	if got := dockerExec(t, id, "cat /e2e-localenv-mount/marker"); got != "mounted-from-localenv" {
		t.Errorf("Feature mount ${localEnv} not resolved on the host running up: %q", got)
	}
	out, err := exec.Command("sh", "-c", `docker image inspect -f '{{index .Config.Labels "devcontainer.metadata"}}' "$(docker inspect -f '{{.Image}}' `+id+`)"`).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect the container's image: %v\n%s", err, out)
	}
	if label := string(out); !strings.Contains(label, `"source":"${localEnv:E2E_LOCALENV_MOUNT}"`) || strings.Contains(label, mountSrc) {
		t.Errorf("image label = %s, want the Feature's mount source raw", label)
	}
}

func TestE2E_RemoveExistingBeforeInitialize(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu","overrideCommand":true}`,
	})

	upOut := runCLI(t, "up", "--workspace-folder", ws, "--skip-post-create")
	assertOutcome(t, upOut, "success")
	oldID, _ := parseJSON(t, upOut)["containerId"].(string)
	if oldID == "" {
		t.Fatal("no containerId")
	}
	defer removeByID(oldID)

	if err := os.WriteFile(filepath.Join(ws, ".devcontainer", "devcontainer.json"),
		[]byte(`{"image":"mcr.microsoft.com/devcontainers/base:ubuntu","overrideCommand":true,"initializeCommand":"exit 1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	upOut2 := runCLI(t, "up", "--workspace-folder", ws, "--remove-existing-container", "--skip-post-create")
	if oc := parseJSON(t, upOut2)["outcome"]; oc != "error" {
		t.Fatalf("second up outcome = %v, want error (initializeCommand fails)", oc)
	}

	if exec.Command("docker", "inspect", oldID).Run() == nil {
		t.Errorf("#844 old container %s still exists after a failed rebuild; removal did not precede initializeCommand", oldID[:12])
	}
}

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
	img, _ := parseJSON(t, buildOut)["imageName"].(string)
	if img == "" {
		t.Fatalf("imageName is not the image's name as a string:\n%s", buildOut)
	}
	defer exec.Command("docker", "rmi", "-f", img).Run()

	out, err := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "e2e.compose.label"}}`, img).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s failed: %v\n%s", img, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "applied" {
		t.Errorf("#930 image %s label e2e.compose.label = %q, want applied (dropped without --image-name)", img, got)
	}
}

func TestE2E_SiblingDockerignore(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json":       `{"build":{"dockerfile":"Dockerfile"}}`,
		".devcontainer/Dockerfile":              "FROM mcr.microsoft.com/devcontainers/base:ubuntu\nCOPY . /ctx-check/\nRUN if [ -e /ctx-check/secret.txt ]; then echo LEAKED > /dockerignore-result; else echo IGNORED_OK > /dockerignore-result; fi\n",
		".devcontainer/Dockerfile.dockerignore": "secret.txt\n",
		".devcontainer/secret.txt":              "SUPERSECRET\n",
	})

	image := uniqueImageName(t, "e2e-dockerignore")
	buildOut := runCLI(t, "build", "--workspace-folder", ws, "--image-name", image)
	assertOutcome(t, buildOut, "success")

	out, err := exec.Command("docker", "run", "--rm", image, "cat", "/dockerignore-result").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "IGNORED_OK" {
		t.Errorf("#969 sibling .dockerignore not applied on rename: got %q (secret.txt leaked into the context)", got)
	}
}

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

	out, err := exec.Command("docker", "exec", id, "sh", "-c", "echo x > /ro-mount/probe 2>&1; echo rc=$?").CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); !strings.Contains(got, "Read-only file system") || strings.Contains(got, "rc=0") {
		t.Errorf("#881 mount object readonly:true did not produce a read-only mount: %q", got)
	}
}

func TestE2E_UpdateUIDSystemUser(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	if os.Getuid() == 0 {
		t.Skip("updateRemoteUserUID is a no-op when the host runs as root")
	}
	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{"build":{"dockerfile":"Dockerfile"},"remoteUser":"svcuser","updateRemoteUserUID":true,"overrideCommand":true}`,
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
