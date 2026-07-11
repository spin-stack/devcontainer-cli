package docker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/log"
)

// --- mock implementation of DockerAPI ---

type mockAPI struct {
	containerCreateFn  func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	containerInspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
	containerListFn    func(ctx context.Context, opts container.ListOptions) ([]container.Summary, error)
	containerRemoveFn  func(ctx context.Context, id string, opts container.RemoveOptions) error
	containerStartFn   func(ctx context.Context, id string, opts container.StartOptions) error
	eventsFn           func(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error)
	imageInspectFn     func(ctx context.Context, id string, opts ...dockerclient.ImageInspectOption) (image.InspectResponse, error)
	imagePullFn        func(ctx context.Context, ref string, opts image.PullOptions) (io.ReadCloser, error)
	imageTagFn         func(ctx context.Context, source, target string) error
	pingFn             func(ctx context.Context) (types.Ping, error)
	serverVersionFn    func(ctx context.Context) (types.Version, error)
}

func (m *mockAPI) Close() error { return nil }

func (m *mockAPI) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	if m.containerCreateFn != nil {
		return m.containerCreateFn(ctx, config, hostConfig, networkingConfig, platform, containerName)
	}
	return container.CreateResponse{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerStart(ctx context.Context, id string, opts container.StartOptions) error {
	if m.containerStartFn != nil {
		return m.containerStartFn(ctx, id, opts)
	}
	return errors.New("not implemented")
}

func (m *mockAPI) ImagePull(ctx context.Context, ref string, opts image.PullOptions) (io.ReadCloser, error) {
	if m.imagePullFn != nil {
		return m.imagePullFn(ctx, ref, opts)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockAPI) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.containerInspectFn != nil {
		return m.containerInspectFn(ctx, id)
	}
	return container.InspectResponse{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
	if m.containerListFn != nil {
		return m.containerListFn(ctx, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAPI) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if m.containerRemoveFn != nil {
		return m.containerRemoveFn(ctx, id, opts)
	}
	return errors.New("not implemented")
}

func (m *mockAPI) Events(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error) {
	if m.eventsFn != nil {
		return m.eventsFn(ctx, opts)
	}
	ch := make(chan events.Message)
	errCh := make(chan error, 1)
	close(ch)
	return ch, errCh
}

func (m *mockAPI) ImageInspect(ctx context.Context, id string, opts ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	if m.imageInspectFn != nil {
		return m.imageInspectFn(ctx, id, opts...)
	}
	return image.InspectResponse{}, errors.New("not implemented")
}

func (m *mockAPI) ImageTag(ctx context.Context, source, target string) error {
	if m.imageTagFn != nil {
		return m.imageTagFn(ctx, source, target)
	}
	return errors.New("not implemented")
}

func (m *mockAPI) Ping(ctx context.Context) (types.Ping, error) {
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return types.Ping{}, nil
}

func (m *mockAPI) ServerVersion(ctx context.Context) (types.Version, error) {
	if m.serverVersionFn != nil {
		return m.serverVersionFn(ctx)
	}
	return types.Version{}, errors.New("not implemented")
}

// --- tests ---

func newTestEngine(api *mockAPI) *EngineClient {
	return &EngineClient{API: api, Log: log.Null}
}

func TestInspectContainer(t *testing.T) {
	api := &mockAPI{
		containerInspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:   id,
					Name: "/my-container",
				},
			}, nil
		},
	}
	e := newTestEngine(api)
	resp, err := e.InspectContainer(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", resp.ID)
	}
	if resp.Name != "/my-container" {
		t.Errorf("Name = %q, want /my-container", resp.Name)
	}
}

func TestInspectContainers(t *testing.T) {
	calls := 0
	api := &mockAPI{
		containerInspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			calls++
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{ID: id},
			}, nil
		},
	}
	e := newTestEngine(api)
	results, err := e.InspectContainers(context.Background(), "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if calls != 3 {
		t.Errorf("API called %d times, want 3", calls)
	}
}

func TestInspectImage(t *testing.T) {
	api := &mockAPI{
		imageInspectFn: func(_ context.Context, id string, _ ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
			return image.InspectResponse{
				ID:           "sha256:abc",
				Architecture: "amd64",
				Os:           "linux",
				Variant:      "v8",
			}, nil
		},
	}
	e := newTestEngine(api)
	resp, err := e.InspectImage(context.Background(), "myimage:latest")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Architecture != "amd64" {
		t.Errorf("Architecture = %q", resp.Architecture)
	}
	if resp.Variant != "v8" {
		t.Errorf("Variant = %q, want v8", resp.Variant)
	}
}

func TestListContainers(t *testing.T) {
	api := &mockAPI{
		containerListFn: func(_ context.Context, opts container.ListOptions) ([]container.Summary, error) {
			if !opts.All {
				t.Error("expected All=true")
			}
			return []container.Summary{
				{ID: "id1"},
				{ID: "id2"},
			}, nil
		},
	}
	e := newTestEngine(api)
	ids, err := e.ListContainers(context.Background(), true, []string{"devcontainer.local_folder=/project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "id1" || ids[1] != "id2" {
		t.Errorf("ids = %v", ids)
	}
}

func TestRemoveContainer_Success(t *testing.T) {
	api := &mockAPI{
		containerRemoveFn: func(_ context.Context, _ string, _ container.RemoveOptions) error {
			return nil
		},
	}
	e := newTestEngine(api)
	if err := e.RemoveContainer(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveContainer_RetryOnAlreadyInProgress(t *testing.T) {
	calls := 0
	api := &mockAPI{
		containerRemoveFn: func(_ context.Context, _ string, _ container.RemoveOptions) error {
			calls++
			if calls <= 2 {
				return errors.New("removal of container xyz is already in progress")
			}
			return nil
		},
		eventsFn: func(_ context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
			ch := make(chan events.Message)
			errCh := make(chan error, 1)
			// Don't send any events — let it timeout and retry
			return ch, errCh
		},
	}
	e := newTestEngine(api)
	if err := e.RemoveContainer(context.Background(), "xyz"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("remove called %d times, want 3", calls)
	}
}

func TestRemoveContainer_DestroyEventStopsRetry(t *testing.T) {
	calls := 0
	api := &mockAPI{
		containerRemoveFn: func(_ context.Context, _ string, _ container.RemoveOptions) error {
			calls++
			return errors.New("removal of container xyz is already in progress")
		},
		eventsFn: func(_ context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
			ch := make(chan events.Message, 1)
			errCh := make(chan error, 1)
			// Immediately send destroy event
			ch <- events.Message{Action: events.ActionDestroy}
			return ch, errCh
		},
	}
	e := newTestEngine(api)
	if err := e.RemoveContainer(context.Background(), "xyz"); err != nil {
		t.Fatal(err)
	}
	// Should have called remove once, then got "already in progress", then got the destroy event
	if calls != 1 {
		t.Errorf("remove called %d times, want 1", calls)
	}
}

func TestIsPodman_Docker(t *testing.T) {
	api := &mockAPI{
		serverVersionFn: func(_ context.Context) (types.Version, error) {
			return types.Version{
				Components: []types.ComponentVersion{
					{Name: "Engine", Version: "24.0.0"},
				},
			}, nil
		},
	}
	e := newTestEngine(api)
	if e.IsPodman(context.Background()) {
		t.Error("expected false for Docker")
	}
}

func TestIsPodman_Podman(t *testing.T) {
	api := &mockAPI{
		serverVersionFn: func(_ context.Context) (types.Version, error) {
			return types.Version{
				Components: []types.ComponentVersion{
					{Name: "Podman", Version: "4.0.0"},
				},
			}, nil
		},
	}
	e := newTestEngine(api)
	if !e.IsPodman(context.Background()) {
		t.Error("expected true for Podman")
	}
}

func TestIsPodman_Error(t *testing.T) {
	api := &mockAPI{
		serverVersionFn: func(_ context.Context) (types.Version, error) {
			return types.Version{}, errors.New("connection refused")
		},
	}
	e := newTestEngine(api)
	if e.IsPodman(context.Background()) {
		t.Error("expected false on error")
	}
}

func TestImageTag(t *testing.T) {
	tagged := false
	api := &mockAPI{
		imageTagFn: func(_ context.Context, source, target string) error {
			if source != "src:v1" || target != "dst:v2" {
				t.Errorf("tag(%q, %q)", source, target)
			}
			tagged = true
			return nil
		},
	}
	e := newTestEngine(api)
	if err := e.ImageTag(context.Background(), "src:v1", "dst:v2"); err != nil {
		t.Fatal(err)
	}
	if !tagged {
		t.Error("ImageTag not called")
	}
}

func TestToDockerImageName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyImage", "myimage"},
		{"my/image", "myimage"},
		{"my image", "myimage"},
		{"valid-name.v1", "valid-name.v1"},
		{"UPPER_CASE", "upper_case"},
		{"dots...dashes---", "dots.dashes---"},
		{"a__b", "a__b"},
		{"a._b", "a.b"},
		{"a-.b", "a-b"},
		{"", ""},
	}
	for _, tt := range tests {
		got := ToDockerImageName(tt.input)
		if got != tt.want {
			t.Errorf("ToDockerImageName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEvents(t *testing.T) {
	api := &mockAPI{
		eventsFn: func(ctx context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
			ch := make(chan events.Message, 2)
			errCh := make(chan error, 1)
			ch <- events.Message{Action: events.ActionStart}
			ch <- events.Message{Action: events.ActionDie}
			close(ch)
			return ch, errCh
		},
	}
	e := newTestEngine(api)
	ctx := context.Background()
	msgCh, errCh := e.Events(ctx, map[string][]string{"container": {"abc"}})
	var msgs []events.Message
	for msg := range msgCh {
		msgs = append(msgs, msg)
	}
	select {
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	default:
	}
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}
