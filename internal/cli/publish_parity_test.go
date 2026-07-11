package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestFeaturesPublishParity publishes the same feature collection through the TS
// oracle and the Go CLI into a throwaway registry:3 container (via testcontainers)
// under separate namespaces, and asserts the normalized result JSON matches. The
// per-feature layer digest differs (tar headers embed non-deterministic mtimes) but
// is scrubbed by normalizeOutput, so publishedTags + version are what get compared.
func TestFeaturesPublishParity(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Needs the compiled TS oracle, the Go binary and Docker.
	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference not compiled")
	}
	goCLI := filepath.Join(repoRoot, "devcontainer")
	if _, err := os.Stat(goCLI); err != nil {
		t.Skip("Go CLI not built")
	}
	if !isDockerAvailable() {
		t.Skip("docker required")
	}

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "registry:3",
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor:   wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStartupTimeout(60 * time.Second),
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

	collection := filepath.Join(repoRoot, "reference", "src", "test", "container-features", "example-v2-features-sets", "simple", "src")

	publish := func(cli []string, namespace string) string {
		args := append([]string{}, cli[1:]...)
		args = append(args, "features", "publish", collection, "-r", registry, "-n", namespace)
		cmd := exec.CommandContext(ctx, cli[0], args...)
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("publish %v failed: %v", cli, err)
		}
		return string(out)
	}

	tsOut := normalizeOutput(publish([]string{"node", filepath.Join(repoRoot, "reference", "devcontainer.js")}, "tsns/features"))
	goOut := normalizeOutput(publish([]string{goCLI}, "gons/features"))

	if tsOut != goOut {
		t.Errorf("publish result differs:\n--- TS\n%s\n--- Go\n%s", tsOut, goOut)
	}
}
