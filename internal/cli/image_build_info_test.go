package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// fakeImages backs an imageInspector: images present locally, images the
// registry knows, and images a pull makes local.
type fakeImages struct {
	local, registry, pullable map[string]*oci.ImageConfig
	pullFailures              int // pulls that fail before one succeeds
	localCalls, registryCalls int
	pulls                     int
}

func (f *fakeImages) inspector() *imageInspector {
	return &imageInspector{
		log: log.Null,
		local: func(_ context.Context, name string) (*oci.ImageConfig, error) {
			f.localCalls++
			if c, ok := f.local[name]; ok {
				return c, nil
			}
			return nil, errors.New("No such image: " + name)
		},
		registry: func(_ context.Context, name string) (*oci.ImageConfig, error) {
			f.registryCalls++
			if c, ok := f.registry[name]; ok {
				return c, nil
			}
			return nil, errors.New("No manifest found for " + name + ".")
		},
		pull: func(_ context.Context, name string) error {
			f.pulls++
			if f.pulls <= f.pullFailures {
				return errors.New("pull flake")
			}
			c, ok := f.pullable[name]
			if !ok {
				return errors.New("pull access denied for " + name)
			}
			if f.local == nil {
				f.local = map[string]*oci.ImageConfig{}
			}
			f.local[name] = c
			return nil
		},
		pullAttempts: 5,
	}
}

func metaImage(user, remoteUser string) *oci.ImageConfig {
	return &oci.ImageConfig{User: user, Labels: map[string]string{"devcontainer.metadata": `[{"remoteUser":"` + remoteUser + `"}]`}}
}

func TestImageInspectorFallbackChain(t *testing.T) {
	ctx := context.Background()

	t.Run("local", func(t *testing.T) {
		f := &fakeImages{local: map[string]*oci.ImageConfig{"img": {User: "local"}}}
		got, err := f.inspector().inspect(ctx, "img")
		if err != nil || got.User != "local" || f.registryCalls != 0 || f.pulls != 0 {
			t.Fatalf("got %+v, %v (registry %d, pulls %d)", got, err, f.registryCalls, f.pulls)
		}
	})
	t.Run("registry without pulling", func(t *testing.T) {
		f := &fakeImages{registry: map[string]*oci.ImageConfig{"img": {User: "remote"}}}
		got, err := f.inspector().inspect(ctx, "img")
		if err != nil || got.User != "remote" || f.pulls != 0 {
			t.Fatalf("got %+v, %v (pulls %d)", got, err, f.pulls)
		}
	})
	t.Run("pull with retries then inspect", func(t *testing.T) {
		f := &fakeImages{pullable: map[string]*oci.ImageConfig{"img": {User: "pulled"}}, pullFailures: 2}
		got, err := f.inspector().inspect(ctx, "img")
		if err != nil || got.User != "pulled" || f.pulls != 3 || f.localCalls != 2 {
			t.Fatalf("got %+v, %v (pulls %d, local %d)", got, err, f.pulls, f.localCalls)
		}
	})
	t.Run("pull failure is an error", func(t *testing.T) {
		f := &fakeImages{}
		got, err := f.inspector().inspect(ctx, "img")
		if err == nil || got != nil || f.pulls != 5 {
			t.Fatalf("got %+v, %v (pulls %d), want an error after 5 pull attempts", got, err, f.pulls)
		}
	})
}

// globalBuildxPlatformArgs feeds docker.Resolver the automatic platform ARGs
// (its resolution is asserted against TS in testdata/ts-oracle/dockerfile.json).
func TestGlobalBuildxPlatformArgs(t *testing.T) {
	got := globalBuildxPlatformArgs(ocispec.Platform{OS: "linux", Architecture: "amd64"}, ocispec.Platform{OS: "linux", Architecture: "arm", Variant: "v7"})
	want := map[string]string{
		"BUILDPLATFORM": "linux/amd64", "BUILDOS": "linux", "BUILDARCH": "amd64", "BUILDVARIANT": "",
		"TARGETPLATFORM": "linux/arm/v7", "TARGETOS": "linux", "TARGETARCH": "arm", "TARGETVARIANT": "v7",
	}
	if !maps.Equal(got, want) {
		t.Errorf("globalBuildxPlatformArgs = %v, want %v", got, want)
	}
}

func writeConfigFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func noCompose(context.Context, []string, string) (map[string]any, error) {
	return nil, errors.New("compose not expected")
}

func TestImageBuildInfoDockerfile(t *testing.T) {
	dir := writeConfigFixture(t, map[string]string{"Dockerfile": "ARG V=1\nFROM base:${V}\n"})
	cfg := &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), Build: &config.Build{Dockerfile: "Dockerfile", Args: map[string]string{"V": "2"}}}
	f := &fakeImages{registry: map[string]*oci.ImageConfig{"base:2": metaImage("node", "node")}}
	info, err := getImageBuildInfo(context.Background(), cfg, f.inspector(), noCompose, log.Null)
	if err != nil {
		t.Fatalf("getImageBuildInfo: %v", err)
	}
	if info.User != "node" || len(info.Metadata) != 1 || info.Metadata[0].RemoteUser != "node" {
		t.Fatalf("info = %+v", info)
	}
	if f.pulls != 0 {
		t.Errorf("pulled %d times; the registry config is enough", f.pulls)
	}
}

func TestImageBuildInfoDockerfileUserOverridesImage(t *testing.T) {
	dir := writeConfigFixture(t, map[string]string{"Dockerfile": "FROM base\nUSER app\n"})
	cfg := &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), DockerFile: "Dockerfile"}
	f := &fakeImages{local: map[string]*oci.ImageConfig{"base": metaImage("node", "node")}}
	info, err := getImageBuildInfo(context.Background(), cfg, f.inspector(), noCompose, log.Null)
	if err != nil || info.User != "app" {
		t.Fatalf("info = %+v, %v; want user app", info, err)
	}
}

func TestImageBuildInfoErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.Dockerfile")
	for _, tc := range []struct {
		name    string
		cfg     *config.DevContainer
		compose composeConfigFunc
		want    string
	}{
		{"dockerfile not found", &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), Build: &config.Build{Dockerfile: "missing.Dockerfile"}}, noCompose, "Dockerfile (" + missing + ") not found."},
		{"no image", &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json")}, noCompose, "No image information specified in devcontainer.json."},
		{"service not found", &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: []string{"compose.yml"}, Service: "app"},
			func(context.Context, []string, string) (map[string]any, error) {
				return map[string]any{"services": map[string]any{"db": map[string]any{"image": "postgres"}}}, nil
			}, "Service 'app' configured in devcontainer.json not found in Docker Compose configuration."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := getImageBuildInfo(context.Background(), tc.cfg, (&fakeImages{}).inspector(), tc.compose, log.Null)
			if err == nil || err.Error() != tc.want || info != nil {
				t.Fatalf("got %+v, %v; want error %q", info, err, tc.want)
			}
		})
	}
}

func TestImageBuildInfoCompose(t *testing.T) {
	dir := writeConfigFixture(t, map[string]string{"compose.yml": "services: {}\n", "build/Dockerfile.dev": "ARG TAG=1\nFROM svc-base:${TAG}\n"})
	cfg := &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: []string{"compose.yml"}, Service: "app"}
	f := &fakeImages{registry: map[string]*oci.ImageConfig{"svc-base:3": metaImage("", "built"), "svc-image:1": metaImage("img", "image")}}

	build := func(context.Context, []string, string) (map[string]any, error) {
		return map[string]any{"services": map[string]any{"app": map[string]any{
			"build": map[string]any{"context": filepath.Join(dir, "build"), "dockerfile": "Dockerfile.dev", "args": map[string]any{"TAG": "3"}},
		}}}, nil
	}
	info, err := getImageBuildInfo(context.Background(), cfg, f.inspector(), build, log.Null)
	if err != nil || info.User != "root" || len(info.Metadata) != 1 || info.Metadata[0].RemoteUser != "built" {
		t.Fatalf("build service: info = %+v, %v", info, err)
	}

	image := func(context.Context, []string, string) (map[string]any, error) {
		return map[string]any{"services": map[string]any{"app": map[string]any{"image": "svc-image:1"}}}, nil
	}
	info, err = getImageBuildInfo(context.Background(), cfg, f.inspector(), image, log.Null)
	if err != nil || info.User != "img" || len(info.Metadata) != 1 || info.Metadata[0].RemoteUser != "image" {
		t.Fatalf("image service: info = %+v, %v", info, err)
	}
}

func TestImageBuildInfoImagePullFailurePropagates(t *testing.T) {
	cfg := &config.DevContainer{ConfigFilePath: "/x/devcontainer.json", Image: "nowhere:1"}
	info, err := getImageBuildInfo(context.Background(), cfg, (&fakeImages{}).inspector(), noCompose, log.Null)
	if err == nil || info != nil {
		t.Fatalf("got %+v, %v; want the pull error", info, err)
	}
}

// The platform ARGs up and build resolve a Dockerfile's FROM with target the
// --platform value (os/arch[/variant]), else this host, as TS
// devContainers.ts targetPlatformInfo; the build platform is always the host.
func TestBuildxPlatformArgs(t *testing.T) {
	host := runtime.GOOS + "/" + runtime.GOARCH
	if got := buildxPlatformArgs(""); got["TARGETPLATFORM"] != host || got["BUILDPLATFORM"] != host || got["TARGETARCH"] != runtime.GOARCH {
		t.Errorf("no --platform: %v, want the host's", got)
	}
	got := buildxPlatformArgs("linux/arm64/v8")
	if got["TARGETPLATFORM"] != "linux/arm64/v8" || got["TARGETOS"] != "linux" || got["TARGETARCH"] != "arm64" || got["TARGETVARIANT"] != "v8" || got["BUILDPLATFORM"] != host {
		t.Errorf("--platform linux/arm64/v8: %v", got)
	}
	if got := buildxPlatformArgs("linux/amd64"); got["TARGETARCH"] != "amd64" || got["TARGETVARIANT"] != "" {
		t.Errorf("--platform linux/amd64: %v", got)
	}
}
