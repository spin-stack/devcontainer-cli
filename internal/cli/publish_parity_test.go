package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPublishParity(t *testing.T) {
	repoRoot := findRepoRoot(t)

	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference not compiled")
	}
	goCLI := envOr("CLI_GO", filepath.Join(repoRoot, "devcontainer"))
	if _, err := os.Stat(goCLI); err != nil {
		t.Skip("Go CLI not built")
	}
	t.Run("tag failure", func(t *testing.T) { publishTagFailureParity(t, repoRoot, goCLI) })

	if !isDockerAvailable() {
		t.Skip("docker required")
	}

	ctx := t.Context()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Image:        "registry:3",
		ExposedPorts: []string{"5000/tcp"},
		WaitingFor:   wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStartupTimeout(60 * time.Second),
		Started:      true,
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
		subcommand string
		collection string
		itemIDs    []string
	}{
		{"features", "features", filepath.Join(repoRoot, "reference", "src", "test", "container-features", "example-v2-features-sets", "simple", "src"), []string{"color", "hello"}},
		{"templates", "templates", filepath.Join(repoRoot, "reference", "src", "test", "container-templates", "example-templates-sets", "simple", "src"), []string{"alpine", "node-mongo", "cpp", "mytemplate"}},
	}

	for _, tc := range cases {
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

			client := oci.NewClientWithAuthPolicy(log.Null, osEnvMap(), oci.DefaultAuthPolicy())
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

func publishTagFailureParity(t *testing.T, repoRoot, goCLI string) {
	for _, collectionType := range []string{"feature", "template"} {
		t.Run(collectionType, func(t *testing.T) {
			reg := newPushRegistry(t, "1.0")
			src := writeTagFailureCollection(t, collectionType)
			type side struct {
				stdout   string
				errLines []string
				exit     int
				attempts []string
				metadata []string
			}
			publish := func(cli []string, namespace string) side {
				args := append(slices.Clone(cli[1:]), collectionType+"s", "publish", src, "-r", reg.host, "-n", namespace)
				cmd := exec.CommandContext(t.Context(), cli[0], args...)
				cmd.Dir = repoRoot
				cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+t.TempDir())
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				var exitErr *exec.ExitError
				if err != nil && !errors.As(err, &exitErr) {
					t.Fatalf("run %v: %v", cli, err)
				}
				var errLines []string
				for line := range strings.Lines(strings.ReplaceAll(stderr.String(), namespace, "<ns>")) {
					for _, marker := range []string{"(!) ERR:", "Failed to PUT manifest for tag"} {
						if i := strings.Index(line, marker); i >= 0 {
							errLines = append(errLines, strings.TrimSpace(line[i:]))
						}
					}
				}
				return side{
					stdout:   stdout.String(),
					errLines: errLines,
					exit:     cmd.ProcessState.ExitCode(),
					attempts: reg.tagAttempts(namespace + "/good"),
					metadata: reg.tagAttempts(namespace),
				}
			}

			ts := publish([]string{"node", filepath.Join(repoRoot, "reference", "devcontainer.js")}, "tsns/coll")
			goSide := publish([]string{goCLI}, "gons/coll")
			if !reflect.DeepEqual(ts, goSide) {
				t.Errorf("publish with a rejected tag differs:\n--- TS\n%+v\n--- Go\n%+v", ts, goSide)
			}
			if ts.exit != 1 || !slices.Equal(ts.attempts, []string{"1", "1.0"}) {
				t.Errorf("TS side = %+v: the fake registry did not exercise the failed tag", ts)
			}
		})
	}
}
