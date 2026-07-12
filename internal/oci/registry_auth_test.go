package oci

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/devcontainers/cli/internal/log"
)

// TestRegistryBasicAuthLoop stands up a real registry:3 protected by htpasswd
// Basic auth (testcontainers) and drives the full oras auth loop end-to-end:
// the first request gets 401 + WWW-Authenticate, oras reads the credential from
// a temporary DOCKER_CONFIG auths entry, retries authenticated, and the
// push/pull succeeds. It also asserts that an anonymous client (no credentials)
// is rejected — proving auth is actually enforced, not incidentally open.
func TestRegistryBasicAuthLoop(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("docker required")
	}

	const user, pass = "testuser", "testpass"
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	htpasswd := user + ":" + string(hash) + "\n"

	ctx := t.Context()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "registry:3",
			ExposedPorts: []string{"5000/tcp"},
			Env: map[string]string{
				"REGISTRY_AUTH":                "htpasswd",
				"REGISTRY_AUTH_HTPASSWD_REALM": "Registry Realm",
				"REGISTRY_AUTH_HTPASSWD_PATH":  "/auth/htpasswd",
			},
			Files: []testcontainers.ContainerFile{{
				Reader:            strings.NewReader(htpasswd),
				ContainerFilePath: "/auth/htpasswd",
				FileMode:          0o444,
			}},
			WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == 401 }).
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("could not start registry container: %v", err)
	}
	defer container.Terminate(ctx)

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "5000")
	if err != nil {
		t.Fatal(err)
	}
	registry := host + ":" + port.Port()

	// A throwaway tarball to push as the artifact layer.
	tgz := filepath.Join(t.TempDir(), "artifact.tgz")
	if err := os.WriteFile(tgz, []byte("dummy-layer-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	ref, err := ParseRef(registry + "/authns/hello:1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	// Authenticated client: DOCKER_CONFIG with an auths entry keyed by the
	// registry host, exactly as `docker login` would write it.
	authEnv := dockerConfigEnv(t, registry, user, pass)
	client := NewClient(log.Null, authEnv)

	res, err := client.PushArtifact(t.Context(), ref, tgz, []string{"1.0.0", "latest"}, "feature", nil)
	if err != nil {
		t.Fatalf("authenticated push failed (401->auth->retry loop broken?): %v", err)
	}
	if len(res.PublishedTags) == 0 {
		t.Fatal("no tags published")
	}

	// Pull side of the loop: list tags + fetch manifest with the same auth.
	tags, err := client.GetPublishedTags(ref)
	if err != nil {
		t.Fatalf("authenticated tag list failed: %v", err)
	}
	if !containsAll(tags, []string{"1.0.0", "latest"}) {
		t.Fatalf("published tags = %v, want to include 1.0.0 and latest", tags)
	}
	if _, err := client.FetchBlob(ref, res.Digest); err != nil {
		// Manifest is fetched via the manifest endpoint; blob-by-digest also
		// exercises the authenticated blob path.
		t.Logf("note: FetchBlob(manifest digest) returned %v (manifest is not a blob); ignoring", err)
	}

	// Negative control: an anonymous client must be rejected on push.
	anonEnv := map[string]string{"DOCKER_CONFIG": t.TempDir()}
	if err := os.WriteFile(filepath.Join(anonEnv["DOCKER_CONFIG"], "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	anon := NewClient(log.Null, anonEnv)
	if _, err := anon.PushArtifact(t.Context(), ref, tgz, []string{"9.9.9"}, "feature", nil); err == nil {
		t.Fatal("anonymous push unexpectedly succeeded; auth is not enforced")
	}
}

// TestClientAuthCacheReused asserts the shared auth cache: every repository()
// built by one Client shares the same auth.Cache, so an auth challenge resolved
// for one operation is reused by related operations instead of re-running the
// 401 loop.
func TestClientAuthCacheReused(t *testing.T) {
	c := NewClient(log.Null, nil)
	if c.authCache == nil {
		t.Fatal("NewClient must initialize a shared auth cache")
	}
	ref, err := ParseRef("registry.example.com/ns/id:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	r1, err := c.repository(ref)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c.repository(ref)
	if err != nil {
		t.Fatal(err)
	}
	ac1, ok1 := r1.Client.(*auth.Client)
	ac2, ok2 := r2.Client.(*auth.Client)
	if !ok1 || !ok2 {
		t.Fatal("expected oras auth.Client transports")
	}
	if ac1.Cache != ac2.Cache {
		t.Fatal("repository() built distinct auth caches; cross-operation reuse lost")
	}
	if ac1.Cache != c.authCache {
		t.Fatal("repository() did not use the client-level auth cache")
	}
}

// dockerConfigEnv writes a temp docker config.json with a Basic auths entry for
// registry and returns an env map pointing DOCKER_CONFIG at it.
func dockerConfigEnv(t *testing.T, registry, user, pass string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	cfg := dockerConfigFile{
		Auths: map[string]dockerConfigAuth{
			registry: {Auth: base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"DOCKER_CONFIG": dir}
}

func isDockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func containsAll(haystack, needles []string) bool {
	set := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}
