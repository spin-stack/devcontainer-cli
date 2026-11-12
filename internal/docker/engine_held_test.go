package docker

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"

	"github.com/devcontainers/cli/internal/log"
)

func daemonHost(t *testing.T) string {
	t.Helper()
	e, err := NewEngineClient(log.Null)
	if err != nil {
		t.Fatalf("NewEngineClient: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e.API.(*mobyclient.Client).DaemonHost()
}

func isolateDockerEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION"} {
		t.Setenv(k, "")
	}
	t.Setenv("DOCKER_CONFIG", dir)
	return dir
}

func TestNewEngineClientDialsTheDockerCLIEndpoint(t *testing.T) {
	t.Run("active context", func(t *testing.T) {
		dir := isolateDockerEnv(t)
		writeContext(t, dir, "remote", "tcp://10.0.0.5:2376")
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"remote"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := daemonHost(t); got != "tcp://10.0.0.5:2376" {
			t.Errorf("host = %q, want the context's tcp://10.0.0.5:2376", got)
		}
	})
	t.Run("DOCKER_HOST wins over the context", func(t *testing.T) {
		dir := isolateDockerEnv(t)
		writeContext(t, dir, "remote", "tcp://10.0.0.5:2376")
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"remote"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
		if got := daemonHost(t); got != "tcp://127.0.0.1:2375" {
			t.Errorf("host = %q, want DOCKER_HOST tcp://127.0.0.1:2375", got)
		}
	})
	t.Run("default context", func(t *testing.T) {
		isolateDockerEnv(t)
		if got := daemonHost(t); got != mobyclient.DefaultDockerHost {
			t.Errorf("host = %q, want %q", got, mobyclient.DefaultDockerHost)
		}
	})
}

func TestRemoveContainerGivesUpAfterSevenTries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		api := &mockAPI{
			containerRemoveFn: func(context.Context, string, mobyclient.ContainerRemoveOptions) error {
				calls++
				return errors.New("Error response from daemon: removal of container xyz is already in progress")
			},
			eventsFn: func(context.Context, mobyclient.EventsListOptions) (<-chan events.Message, <-chan error) {
				return make(chan events.Message), make(chan error)
			},
		}
		err := newTestEngine(api).RemoveContainer(t.Context(), "xyz")
		if err == nil || !strings.Contains(err.Error(), "already in progress") {
			t.Errorf("err = %v, want the last removal error", err)
		}
		if calls != 7 {
			t.Errorf("removal tried %d times, want 7", calls)
		}
	})
}

func TestRemoveContainerReturnsAnyOtherErrorAtOnce(t *testing.T) {
	calls := 0
	api := &mockAPI{
		containerRemoveFn: func(context.Context, string, mobyclient.ContainerRemoveOptions) error {
			calls++
			return errors.New("Error response from daemon: no such container: xyz")
		},
	}
	err := newTestEngine(api).RemoveContainer(t.Context(), "xyz")
	if err == nil || !strings.Contains(err.Error(), "no such container") {
		t.Errorf("err = %v, want the removal error", err)
	}
	if calls != 1 {
		t.Errorf("removal tried %d times, want 1", calls)
	}
}

func TestImageLabelsEnsuringPresentWithoutConfig(t *testing.T) {
	e := newMockEngine(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/images/create") {
			return httpJSON(200, `{"status":"ok"}`), nil
		}
		return httpJSON(200, `{"Id":"sha256:x"}`), nil
	})
	if got := e.ImageLabelsEnsuringPresent(t.Context(), "img"); got != nil {
		t.Errorf("labels = %v, want nil", got)
	}
}

func TestImageLabelsEnsuringPresentWithoutConfigAfterPull(t *testing.T) {
	inspects := 0
	e := newMockEngine(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/images/create") {
			return httpJSON(200, `{"status":"ok"}`), nil
		}
		inspects++
		if inspects == 1 {
			return httpJSON(404, `{"message":"no such image"}`), nil
		}
		return httpJSON(200, `{"Id":"sha256:x"}`), nil
	})
	if got := e.ImageLabelsEnsuringPresent(t.Context(), "img"); got != nil {
		t.Errorf("labels = %v, want nil", got)
	}
	if inspects != 2 {
		t.Errorf("inspected %d times, want 2", inspects)
	}
}

func TestPullImagePlatformSendsThePlatform(t *testing.T) {
	for _, tc := range []struct{ platform, want string }{
		{"linux/arm64/v8", "linux/arm64/v8"},
		{"linux/amd64", "linux/amd64"},
		{"", ""},
	} {
		var got string
		e := newMockEngine(t, func(r *http.Request) (*http.Response, error) {
			got = r.URL.Query().Get("platform")
			return httpJSON(200, `{"status":"ok"}`), nil
		})
		if err := e.PullImagePlatform(t.Context(), "alpine", tc.platform); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("PullImagePlatform(%q) sent platform %q, want %q", tc.platform, got, tc.want)
		}
	}
}

func TestParsePlatformKeepsTheRestAsTheVariant(t *testing.T) {
	if p := parsePlatform("linux/arm/v7/extra"); p.OS != "linux" || p.Architecture != "arm" || p.Variant != "v7/extra" {
		t.Errorf("parsePlatform = %+v, want linux arm v7/extra", p)
	}
}

func TestCreateContainerReturnsTheIDOrTheError(t *testing.T) {
	var gotImage string
	ok := newTestEngine(&mockAPI{containerCreateFn: func(_ context.Context, o mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		gotImage = o.Config.Image
		return mobyclient.ContainerCreateResult{ID: "c0ffee"}, nil
	}})
	id, err := ok.CreateContainer(t.Context(), &container.Config{Image: "alpine"}, &container.HostConfig{})
	if err != nil || id != "c0ffee" || gotImage != "alpine" {
		t.Errorf("CreateContainer = (%q, %v) for image %q, want (c0ffee, nil) for alpine", id, err, gotImage)
	}

	failing := newTestEngine(&mockAPI{containerCreateFn: func(context.Context, mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		return mobyclient.ContainerCreateResult{ID: "partial"}, errors.New("conflict")
	}})
	if id, err := failing.CreateContainer(t.Context(), &container.Config{}, nil); err == nil || id != "" {
		t.Errorf("CreateContainer = (%q, %v), want (\"\", conflict)", id, err)
	}
}
