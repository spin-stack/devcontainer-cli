package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"

	"github.com/devcontainers/cli/internal/log"
)

// --- mock implementation of API ---
//
// The func fields keep the "inner" moby types (container.InspectResponse, …) so
// test setups stay concise; the interface methods wrap them into the v29
// options/result envelopes.

type mockAPI struct {
	containerCreateFn  func(ctx context.Context, opts mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error)
	containerInspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
	containerListFn    func(ctx context.Context, opts mobyclient.ContainerListOptions) ([]container.Summary, error)
	containerRemoveFn  func(ctx context.Context, id string, opts mobyclient.ContainerRemoveOptions) error
	containerStartFn   func(ctx context.Context, id string, opts mobyclient.ContainerStartOptions) error
	containerStopFn    func(ctx context.Context, id string, opts mobyclient.ContainerStopOptions) error
	eventsFn           func(ctx context.Context, opts mobyclient.EventsListOptions) (<-chan events.Message, <-chan error)
	imageInspectFn     func(ctx context.Context, id string, opts ...mobyclient.ImageInspectOption) (image.InspectResponse, error)
	imagePullFn        func(ctx context.Context, ref string, opts mobyclient.ImagePullOptions) (mobyclient.ImagePullResponse, error)
	imageTagFn         func(ctx context.Context, opts mobyclient.ImageTagOptions) error
	serverVersionFn    func(ctx context.Context, opts mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error)
	execCreateFn       func(ctx context.Context, containerID string, opts mobyclient.ExecCreateOptions) (mobyclient.ExecCreateResult, error)
	execAttachFn       func(ctx context.Context, execID string, opts mobyclient.ExecAttachOptions) (mobyclient.ExecAttachResult, error)
	execInspectFn      func(ctx context.Context, execID string) (mobyclient.ExecInspectResult, error)
}

func (m *mockAPI) ExecInspect(ctx context.Context, execID string, _ mobyclient.ExecInspectOptions) (mobyclient.ExecInspectResult, error) {
	if m.execInspectFn != nil {
		return m.execInspectFn(ctx, execID)
	}
	return mobyclient.ExecInspectResult{}, errors.New("not implemented")
}

func (m *mockAPI) ExecCreate(ctx context.Context, containerID string, opts mobyclient.ExecCreateOptions) (mobyclient.ExecCreateResult, error) {
	if m.execCreateFn != nil {
		return m.execCreateFn(ctx, containerID, opts)
	}
	return mobyclient.ExecCreateResult{}, errors.New("not implemented")
}

func (m *mockAPI) ExecAttach(ctx context.Context, execID string, opts mobyclient.ExecAttachOptions) (mobyclient.ExecAttachResult, error) {
	if m.execAttachFn != nil {
		return m.execAttachFn(ctx, execID, opts)
	}
	return mobyclient.ExecAttachResult{}, errors.New("not implemented")
}

func (m *mockAPI) Close() error { return nil }

func (m *mockAPI) ContainerCreate(ctx context.Context, opts mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
	if m.containerCreateFn != nil {
		return m.containerCreateFn(ctx, opts)
	}
	return mobyclient.ContainerCreateResult{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerStart(ctx context.Context, id string, opts mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error) {
	if m.containerStartFn != nil {
		return mobyclient.ContainerStartResult{}, m.containerStartFn(ctx, id, opts)
	}
	return mobyclient.ContainerStartResult{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerStop(ctx context.Context, id string, opts mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
	if m.containerStopFn != nil {
		return mobyclient.ContainerStopResult{}, m.containerStopFn(ctx, id, opts)
	}
	return mobyclient.ContainerStopResult{}, nil
}

func (m *mockAPI) ImagePull(ctx context.Context, ref string, opts mobyclient.ImagePullOptions) (mobyclient.ImagePullResponse, error) {
	if m.imagePullFn != nil {
		return m.imagePullFn(ctx, ref, opts)
	}
	return nil, nil
}

func (m *mockAPI) ContainerInspect(ctx context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	if m.containerInspectFn != nil {
		resp, err := m.containerInspectFn(ctx, id)
		return mobyclient.ContainerInspectResult{Container: resp}, err
	}
	return mobyclient.ContainerInspectResult{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerList(ctx context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	if m.containerListFn != nil {
		items, err := m.containerListFn(ctx, opts)
		return mobyclient.ContainerListResult{Items: items}, err
	}
	return mobyclient.ContainerListResult{}, errors.New("not implemented")
}

func (m *mockAPI) ContainerRemove(ctx context.Context, id string, opts mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
	if m.containerRemoveFn != nil {
		return mobyclient.ContainerRemoveResult{}, m.containerRemoveFn(ctx, id, opts)
	}
	return mobyclient.ContainerRemoveResult{}, errors.New("not implemented")
}

func (m *mockAPI) Events(ctx context.Context, opts mobyclient.EventsListOptions) mobyclient.EventsResult {
	if m.eventsFn != nil {
		msgs, errc := m.eventsFn(ctx, opts)
		return mobyclient.EventsResult{Messages: msgs, Err: errc}
	}
	ch := make(chan events.Message)
	errCh := make(chan error, 1)
	close(ch)
	return mobyclient.EventsResult{Messages: ch, Err: errCh}
}

func (m *mockAPI) ImageInspect(ctx context.Context, id string, opts ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
	if m.imageInspectFn != nil {
		resp, err := m.imageInspectFn(ctx, id, opts...)
		return mobyclient.ImageInspectResult{InspectResponse: resp}, err
	}
	return mobyclient.ImageInspectResult{}, errors.New("not implemented")
}

func (m *mockAPI) ImageTag(ctx context.Context, opts mobyclient.ImageTagOptions) (mobyclient.ImageTagResult, error) {
	if m.imageTagFn != nil {
		return mobyclient.ImageTagResult{}, m.imageTagFn(ctx, opts)
	}
	return mobyclient.ImageTagResult{}, errors.New("not implemented")
}

func (m *mockAPI) ServerVersion(ctx context.Context, opts mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error) {
	if m.serverVersionFn != nil {
		return m.serverVersionFn(ctx, opts)
	}
	return mobyclient.ServerVersionResult{}, errors.New("not implemented")
}

// --- tests ---

func newTestEngine(api *mockAPI) *EngineClient {
	return &EngineClient{API: api, Log: log.Null}
}

func TestInspectContainer(t *testing.T) {
	api := &mockAPI{
		containerInspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{ID: id, Name: "/my-container"}, nil
		},
	}
	e := newTestEngine(api)
	resp, err := e.InspectContainer(t.Context(), "abc123")
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
			return container.InspectResponse{ID: id}, nil
		},
	}
	e := newTestEngine(api)
	results, err := e.InspectContainers(t.Context(), "a", "b", "c")
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
		imageInspectFn: func(_ context.Context, id string, _ ...mobyclient.ImageInspectOption) (image.InspectResponse, error) {
			return image.InspectResponse{
				ID:           "sha256:abc",
				Architecture: "amd64",
				Os:           "linux",
				Variant:      "v8",
			}, nil
		},
	}
	e := newTestEngine(api)
	resp, err := e.InspectImage(t.Context(), "myimage:latest")
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
		containerListFn: func(_ context.Context, opts mobyclient.ContainerListOptions) ([]container.Summary, error) {
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
	ids, err := e.ListContainers(t.Context(), true, []string{"devcontainer.local_folder=/project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "id1" || ids[1] != "id2" {
		t.Errorf("ids = %v", ids)
	}
}

func TestStopContainer(t *testing.T) {
	var gotID string
	api := &mockAPI{containerStopFn: func(_ context.Context, id string, _ mobyclient.ContainerStopOptions) error {
		gotID = id
		return nil
	}}
	if err := newTestEngine(api).StopContainer(t.Context(), "abc123"); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	if gotID != "abc123" {
		t.Errorf("stopped %q, want abc123", gotID)
	}
}

func TestRemoveContainer(t *testing.T) {
	// Each case shares the same call shape (increment counter, return an error
	// derived from the current call count) but varies the retry-driving events
	// stream, so a per-case remove/events closure keeps the mock setup honest.
	tests := []struct {
		name string
		// remove receives the running call count (after increment) and returns
		// the error the mock should surface for that call.
		remove func(calls int) error
		// events, when non-nil, overrides the default (closed-channel) events
		// stream to drive retry/destroy behavior.
		events    func(ctx context.Context, opts mobyclient.EventsListOptions) (<-chan events.Message, <-chan error)
		wantCalls int
	}{
		{
			name:      "Success",
			remove:    func(_ int) error { return nil },
			wantCalls: 1,
		},
		{
			name: "RetryOnAlreadyInProgress",
			remove: func(calls int) error {
				if calls <= 2 {
					return errors.New("removal of container xyz is already in progress")
				}
				return nil
			},
			events: func(_ context.Context, _ mobyclient.EventsListOptions) (<-chan events.Message, <-chan error) {
				ch := make(chan events.Message)
				errCh := make(chan error, 1)
				// Don't send any events — let it timeout and retry
				return ch, errCh
			},
			wantCalls: 3,
		},
		{
			name: "DestroyEventStopsRetry",
			remove: func(_ int) error {
				return errors.New("removal of container xyz is already in progress")
			},
			events: func(_ context.Context, _ mobyclient.EventsListOptions) (<-chan events.Message, <-chan error) {
				ch := make(chan events.Message, 1)
				errCh := make(chan error, 1)
				// Immediately send destroy event
				ch <- events.Message{Action: events.ActionDestroy}
				return ch, errCh
			},
			// Called remove once, got "already in progress", then the destroy event stopped the retry.
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			api := &mockAPI{
				containerRemoveFn: func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) error {
					calls++
					return tt.remove(calls)
				},
			}
			if tt.events != nil {
				api.eventsFn = tt.events
			}
			e := newTestEngine(api)
			if err := e.RemoveContainer(t.Context(), "xyz"); err != nil {
				t.Fatal(err)
			}
			if calls != tt.wantCalls {
				t.Errorf("remove called %d times, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	tagged := false
	api := &mockAPI{
		imageTagFn: func(_ context.Context, opts mobyclient.ImageTagOptions) error {
			if opts.Source != "src:v1" || opts.Target != "dst:v2" {
				t.Errorf("tag(%q, %q)", opts.Source, opts.Target)
			}
			tagged = true
			return nil
		},
	}
	e := newTestEngine(api)
	if err := e.ImageTag(t.Context(), "src:v1", "dst:v2"); err != nil {
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
		got := ToImageName(tt.input)
		if got != tt.want {
			t.Errorf("ToImageName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEvents(t *testing.T) {
	api := &mockAPI{
		eventsFn: func(_ context.Context, _ mobyclient.EventsListOptions) (<-chan events.Message, <-chan error) {
			ch := make(chan events.Message, 2)
			errCh := make(chan error, 1)
			ch <- events.Message{Action: events.ActionStart}
			ch <- events.Message{Action: events.ActionDie}
			close(ch)
			return ch, errCh
		},
	}
	e := newTestEngine(api)
	ctx := t.Context()
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
