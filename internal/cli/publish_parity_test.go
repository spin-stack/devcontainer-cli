package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestPublishParity publishes the same feature/template collection through the TS
// oracle and the Go CLI into a throwaway registry:3 container (via testcontainers)
// under separate namespaces, and asserts the normalized result JSON matches. The
// per-item layer digest differs (tar headers embed non-deterministic mtimes) but is
// scrubbed by normalizeOutput, so publishedTags + version are what get compared.
func TestPublishParity(t *testing.T) {
	repoRoot := findRepoRoot(t)

	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference not compiled")
	}
	goCLI := envOr("CLI_GO", filepath.Join(repoRoot, "devcontainer"))
	if _, err := os.Stat(goCLI); err != nil {
		t.Skip("Go CLI not built")
	}
	if !isDockerAvailable() {
		t.Skip("docker required")
	}

	ctx := t.Context()
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

	cases := []struct {
		name       string
		subcommand string // "features" | "templates"
		collection string
		itemIDs    []string
	}{
		{"features", "features", filepath.Join(repoRoot, "reference", "src", "test", "container-features", "example-v2-features-sets", "simple", "src"), []string{"color", "hello"}},
		{"templates", "templates", filepath.Join(repoRoot, "reference", "src", "test", "container-templates", "example-templates-sets", "simple", "src"), []string{"alpine", "node-mongo", "cpp", "mytemplate"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tsNamespace := "tsns/" + tc.name
			goNamespace := "gons/" + tc.name
			publish := func(cli []string, namespace string) string {
				args := append([]string{}, cli[1:]...)
				args = append(args, tc.subcommand, "publish", tc.collection, "-r", registry, "-n", namespace)
				cmd := exec.CommandContext(ctx, cli[0], args...)
				cmd.Dir = repoRoot
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("publish %v failed: %v", cli, err)
				}
				return string(out)
			}

			tsOut := normalizeOutput(publish([]string{"node", filepath.Join(repoRoot, "reference", "devcontainer.js")}, tsNamespace))
			goOut := normalizeOutput(publish([]string{goCLI}, goNamespace))

			if tsOut != goOut {
				t.Errorf("publish result differs:\n--- TS\n%s\n--- Go\n%s", tsOut, goOut)
			}

			// Output parity alone is insufficient for a mutating command. Verify the
			// registry state for every item and the collection metadata artifact.
			client := oci.NewClient(log.Null, osEnvMap())
			for _, id := range tc.itemIDs {
				tagsFor := func(namespace string) []string {
					ref, err := oci.ParseRef(registry + "/" + namespace + "/" + id)
					if err != nil {
						t.Fatal(err)
					}
					tags, err := client.GetPublishedTags(ref)
					if err != nil {
						t.Fatalf("published tags for %s/%s: %v", namespace, id, err)
					}
					sort.Strings(tags)
					return tags
				}
				tsTags, goTags := tagsFor(tsNamespace), tagsFor(goNamespace)
				if len(goTags) == 0 || !reflect.DeepEqual(tsTags, goTags) {
					t.Errorf("registry tags for %s differ: TS=%v Go=%v", id, tsTags, goTags)
				}
			}
			for _, namespace := range []string{tsNamespace, goNamespace} {
				ref, err := oci.ParseRef(registry + "/" + namespace + ":latest")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.FetchManifest(ref, ""); err != nil {
					t.Errorf("collection metadata was not published for %s: %v", namespace, err)
				}
			}
		})
	}
}
