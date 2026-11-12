package cli

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const e2eBaseImage = "mcr.microsoft.com/devcontainers/base:ubuntu"

// startTLSServer runs `openssl s_server -www` on the default bridge, where
// builds' RUNs and dev containers reach it by its address, with a certificate
// for that address signed by ca. The host's firewall may drop a container's
// traffic to the host, so the server is a container too.
func startTLSServer(t *testing.T, ca *testCA) (id, url string) {
	t.Helper()
	certs := t.TempDir()
	if err := os.Chmod(certs, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "run", "-d", "--rm", "-v", certs+":/certs:ro", e2eBaseImage, "sh", "-c",
		"while [ ! -f /certs/ready ]; do sleep 0.1; done; exec openssl s_server -quiet -www -accept 8443 -cert /certs/cert.pem -key /certs/key.pem").CombinedOutput()
	if err != nil {
		t.Fatalf("start TLS server: %v\n%s", err, out)
	}
	id = strings.TrimSpace(string(out))
	t.Cleanup(func() { removeByID(id) })
	ipOut, err := exec.Command("docker", "inspect", "-f", "{{.NetworkSettings.Networks.bridge.IPAddress}}", id).Output()
	if err != nil {
		t.Fatalf("server address: %v", err)
	}
	ip := net.ParseIP(strings.TrimSpace(string(ipOut)))
	if ip == nil {
		t.Fatalf("server address %q", ipOut)
	}
	certPEM, keyPEM := ca.serverPEM(t, ip)
	writeTestFile(t, filepath.Join(certs, "cert.pem"), certPEM)
	writeTestFile(t, filepath.Join(certs, "key.pem"), keyPEM)
	writeTestFile(t, filepath.Join(certs, "ready"), nil)

	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	addr := net.JoinHostPort(ip.String(), "8443")
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{RootCAs: pool})
		if err == nil {
			conn.Close()
			return id, "https://" + addr + "/"
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS server at %s never answered: %v", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// imageLeaks lists the files of image's own layers among files, and where the
// markers appear in what image adds to base: the content of its own layers'
// files and its config, which holds
// the history and labels. A layer of base is skipped by its diff ID.
func imageLeaks(t *testing.T, image, base string, files []string, markers ...string) []string {
	t.Helper()
	baseLayers := map[string]bool{}
	out, err := exec.Command("docker", "image", "inspect", "-f", "{{json .RootFS.Layers}}", base).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", base, err)
	}
	var diffIDs []string
	if err := json.Unmarshal(out, &diffIDs); err != nil {
		t.Fatal(err)
	}
	for _, d := range diffIDs {
		baseLayers[strings.TrimPrefix(d, "sha256:")] = true
	}

	save := exec.Command("docker", "save", image)
	stdout, err := save.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := save.Start(); err != nil {
		t.Fatal(err)
	}
	var leaks []string
	check := func(where string, data []byte) {
		for _, m := range markers {
			if bytes.Contains(data, []byte(m)) {
				leaks = append(leaks, fmt.Sprintf("%s holds %q", where, m))
			}
		}
	}
	saved := tar.NewReader(stdout)
	for {
		hdr, err := saved.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read docker save: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg || baseLayers[filepath.Base(hdr.Name)] {
			continue
		}
		data, err := io.ReadAll(saved)
		if err != nil {
			t.Fatal(err)
		}
		layer := tar.NewReader(bytes.NewReader(data))
		if _, err := layer.Next(); err != nil {
			check(hdr.Name, data) // config, manifest or index
			continue
		}
		layer = tar.NewReader(bytes.NewReader(data))
		for {
			f, err := layer.Next()
			if err != nil {
				break
			}
			if slices.Contains(files, strings.TrimPrefix(f.Name, "./")) {
				leaks = append(leaks, fmt.Sprintf("%s has the file %s", hdr.Name, f.Name))
			}
			content, _ := io.ReadAll(layer)
			check(hdr.Name+":"+f.Name, content)
		}
	}
	if err := save.Wait(); err != nil {
		t.Fatalf("docker save %s: %v", image, err)
	}
	return leaks
}

func uniqueImageName(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	name := prefix + "-" + hex.EncodeToString(b)
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", name).Run() })
	return name
}

// TestE2E_CABundle: with --ca-bundle a Feature's install and a Dockerfile RUN
// that mounts the secret reach an HTTPS server signed by a CA the image does
// not trust, and fail without it; the image carries neither the bundle nor its
// content; the container trusts the server in curl, node and its lifecycle
// commands; run-user-commands refuses a container created without it.
func TestE2E_CABundle(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	ca := newTestCA(t)
	bundle := writeTestFile(t, filepath.Join(t.TempDir(), "bundle.crt"), ca.pem)
	// A line of the CA's base64 body: in a file, a layer or a config only if
	// the bundle was copied there.
	marker := strings.Split(string(ca.pem), "\n")[1]
	serverID, url := startTLSServer(t, ca)
	// The server answers any path; a path of this run's own keeps BuildKit from
	// reusing a RUN an earlier run built, which would pass without the bundle.
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	url += hex.EncodeToString(nonce)
	reach := "curl -fsS --max-time 10 -o /dev/null " + url

	featureWS := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{
			"image": "` + e2eBaseImage + `",
			"features": { "./reach": {} },
			"postCreateCommand": "` + reach + ` && touch /tmp/post-create-trusted"
		}`,
		".devcontainer/reach/devcontainer-feature.json": `{"id": "reach", "version": "1.0.0", "name": "reach"}`,
		".devcontainer/reach/install.sh":                "#!/bin/sh\nset -e\n" + reach + "\necho reached > /feature-reached\n",
	})
	dockerfileWS := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{"build": {"dockerfile": "Dockerfile"}}`,
		".devcontainer/Dockerfile": "FROM " + e2eBaseImage + "\n" +
			"RUN --mount=type=secret,id=devcontainer-ca-bundle,target=/run/devcontainer/ca-bundle.crt " +
			"curl -fsS --max-time 10 --cacert /run/devcontainer/ca-bundle.crt -o /dev/null " + url + " && echo reached > /dockerfile-reached\n",
	})

	for _, tc := range []struct{ name, ws string }{{"feature", featureWS}, {"dockerfile", dockerfileWS}} {
		t.Run(tc.name, func(t *testing.T) {
			image := uniqueImageName(t, "e2e-ca-bundle-"+tc.name)
			assertOutcome(t, runCLI(t, "build", "--workspace-folder", tc.ws, "--image-name", image), "error")
			assertOutcome(t, runCLI(t, "build", "--workspace-folder", tc.ws, "--image-name", image, "--ca-bundle", bundle), "success")
			if leaks := imageLeaks(t, image, e2eBaseImage, []string{"run/devcontainer/ca-bundle.crt"}, marker); len(leaks) > 0 {
				t.Errorf("the image carries the bundle:\n%s", strings.Join(leaks, "\n"))
			}
			if env, err := exec.Command("docker", "image", "inspect", "-f", "{{json .Config.Env}}", image).Output(); err != nil || strings.Contains(string(env), "ca-bundle") {
				t.Errorf("the image's env names the bundle: %s %v", env, err)
			}
			// A bundle of another CA cannot reach the server, so this build
			// passes only if the bundle's content is outside the cache key, and
			// then it makes the same image.
			// The image's ID names a manifest list whose attestation carries a
			// timestamp; its layers are what the build made.
			before := dockerOutput(t, "image", "inspect", "-f", "{{json .RootFS.Layers}}", image)
			other := writeTestFile(t, filepath.Join(t.TempDir(), "other.crt"), newTestCA(t).pem)
			assertOutcome(t, runCLI(t, "build", "--workspace-folder", tc.ws, "--image-name", image, "--ca-bundle", other), "success")
			if after := dockerOutput(t, "image", "inspect", "-f", "{{json .RootFS.Layers}}", image); after != before {
				t.Errorf("another bundle made layers %s, want %s", after, before)
			}
		})
	}

	t.Run("container", func(t *testing.T) {
		upOut := runCLI(t, "up", "--workspace-folder", featureWS, "--ca-bundle", bundle)
		assertOutcome(t, upOut, "success")
		id, _ := parseJSON(t, upOut)["containerId"].(string)
		t.Cleanup(func() { removeByID(id) })
		dockerExec(t, id, "test -f /feature-reached && test -f /tmp/post-create-trusted && "+reach)
		if out, err := exec.Command("docker", "exec", id, "sh", "-c", "env -u CURL_CA_BUNDLE -u SSL_CERT_FILE "+reach).CombinedOutput(); err == nil {
			t.Errorf("curl trusted the server without the bundle's variables:\n%s", out)
		}
		if out, err := exec.Command("docker", "exec", id, "sh", "-c", "echo x > /run/devcontainer/ca-bundle.crt").CombinedOutput(); err == nil {
			t.Errorf("the bundle is writable in the container:\n%s", out)
		}
		assertOutcome(t, runCLI(t, "run-user-commands", "--container-id", id, "--ca-bundle", bundle), "success")
		refused := runCLI(t, "run-user-commands", "--container-id", serverID, "--ca-bundle", bundle)
		assertOutcome(t, refused, "error")
		if !strings.Contains(refused, "has no CA bundle at /run/devcontainer/ca-bundle.crt") {
			t.Errorf("run-user-commands on a container without the bundle: %s", refused)
		}
	})

	t.Run("node", func(t *testing.T) {
		ws := writeFixture(t, map[string]string{
			".devcontainer/devcontainer.json": `{"image": "mirror.gcr.io/library/node:22-bookworm-slim"}`,
		})
		upOut := runCLI(t, "up", "--workspace-folder", ws, "--ca-bundle", bundle)
		assertOutcome(t, upOut, "success")
		id, _ := parseJSON(t, upOut)["containerId"].(string)
		t.Cleanup(func() { removeByID(id) })
		get := `node -e 'require("https").get("` + url + `", r => process.exit(r.statusCode === 200 ? 0 : 1)).on("error", e => { console.error(e.message); process.exit(1) })'`
		dockerExec(t, id, get)
		if out, err := exec.Command("docker", "exec", id, "sh", "-c", "env -u NODE_EXTRA_CA_CERTS "+get).CombinedOutput(); err == nil {
			t.Errorf("node trusted the server without NODE_EXTRA_CA_CERTS:\n%s", out)
		}
	})
}

// TestE2E_UpSecretsFile: up --secrets-file hands a value to a Dockerfile's
// `RUN --mount=type=secret,id=K` and to postCreateCommand, and the value is in
// no layer, no image config (which holds `docker history`) and no part of the
// container's inspect, its labels included. Both sides record a hash, so the
// test compares without writing the value anywhere itself.
func TestE2E_UpSecretsFile(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	value := "e2e-secret-" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(value))
	wantSum := hex.EncodeToString(sum[:])
	secrets := writeTestFile(t, filepath.Join(t.TempDir(), "secrets.json"), []byte(`{"E2E_SECRET": "`+value+`"}`))

	ws := writeFixture(t, map[string]string{
		".devcontainer/devcontainer.json": `{
			"build": {"dockerfile": "Dockerfile"},
			"postCreateCommand": "printf %s \"$E2E_SECRET\" | sha256sum | cut -d' ' -f1 > /tmp/post-create.sum"
		}`,
		".devcontainer/Dockerfile": "FROM " + e2eBaseImage + "\n" +
			// The run's own hash in the instruction: a secret is outside the
			// cache key, so an earlier run's RUN would otherwise be reused.
			"RUN --mount=type=secret,id=E2E_SECRET sha256sum /run/secrets/E2E_SECRET | cut -d' ' -f1 > /build.sum && echo " + wantSum[:16] + " > /build.run\n",
	})
	upOut := runCLI(t, "up", "--workspace-folder", ws, "--secrets-file", secrets)
	assertOutcome(t, upOut, "success")
	id, _ := parseJSON(t, upOut)["containerId"].(string)
	t.Cleanup(func() { removeByID(id) })

	if got := dockerExec(t, id, "cat /build.sum"); got != wantSum {
		t.Errorf("the Dockerfile RUN read %q, want the secret's hash %q", got, wantSum)
	}
	if got := dockerExec(t, id, "cat /tmp/post-create.sum"); got != wantSum {
		t.Errorf("postCreateCommand read %q, want the secret's hash %q", got, wantSum)
	}
	image := strings.TrimSpace(dockerOutput(t, "inspect", "-f", "{{.Config.Image}}", id))
	if leaks := imageLeaks(t, image, e2eBaseImage, nil, value); len(leaks) > 0 {
		t.Errorf("the image carries the secret:\n%s", strings.Join(leaks, "\n"))
	}
	if strings.Contains(dockerOutput(t, "history", "--no-trunc", image), value) {
		t.Error("docker history shows the secret")
	}
	if strings.Contains(dockerOutput(t, "container", "inspect", id), value) {
		t.Error("the container's inspect, labels included, shows the secret")
	}
}

func dockerOutput(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
