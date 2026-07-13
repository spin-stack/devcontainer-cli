package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/log"
)

// API is the subset of the Docker client API that EngineClient needs.
// Defined as an interface so it can be replaced in tests. It targets the
// moby/moby/client v29 "options-in, result-out" surface (the github.com/docker/docker
// module is deprecated in favor of github.com/moby/moby/{client,api}).
type API interface {
	io.Closer
	ContainerCreate(ctx context.Context, options mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, containerID string, options mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
	ContainerList(ctx context.Context, options mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error)
	ContainerRemove(ctx context.Context, containerID string, options mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error)
	ContainerStart(ctx context.Context, containerID string, options mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error)
	ContainerStop(ctx context.Context, containerID string, options mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error)
	Events(ctx context.Context, options mobyclient.EventsListOptions) mobyclient.EventsResult
	ExecCreate(ctx context.Context, containerID string, options mobyclient.ExecCreateOptions) (mobyclient.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options mobyclient.ExecAttachOptions) (mobyclient.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options mobyclient.ExecInspectOptions) (mobyclient.ExecInspectResult, error)
	ImageInspect(ctx context.Context, imageID string, options ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error)
	ImagePull(ctx context.Context, refStr string, options mobyclient.ImagePullOptions) (mobyclient.ImagePullResponse, error)
	ImageTag(ctx context.Context, options mobyclient.ImageTagOptions) (mobyclient.ImageTagResult, error)
	ServerVersion(ctx context.Context, options mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error)
}

// EngineClient talks to the Docker Engine via the SDK instead of shelling out.
type EngineClient struct {
	API API
	Log log.Logger
}

// NewEngineClient connects to the Docker daemon using environment defaults
// (DOCKER_HOST, DOCKER_TLS_VERIFY, etc.) with automatic API version negotiation.
// When DOCKER_HOST is not set, it resolves the active Docker context endpoint
// to match the behavior of the Docker CLI.
func NewEngineClient(logger log.Logger, extraOpts ...mobyclient.Opt) (*EngineClient, error) {
	// API-version negotiation is enabled by default in the v29 client.
	opts := []mobyclient.Opt{
		mobyclient.FromEnv,
	}

	// When DOCKER_HOST is not set, resolve from the active Docker context
	// so the SDK talks to the same daemon as the Docker CLI.
	if os.Getenv("DOCKER_HOST") == "" {
		if host := resolveDockerContextHost(); host != "" {
			opts = append(opts, mobyclient.WithHost(host))
		}
	}

	opts = append(opts, extraOpts...)

	cli, err := mobyclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker engine client: %w", err)
	}
	return &EngineClient{API: cli, Log: logger}, nil
}

// Close releases the underlying connection.
func (e *EngineClient) Close() error {
	return e.API.Close()
}

// InspectContainer returns the full inspect response for one container.
func (e *EngineClient) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := e.API.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	return res.Container, nil
}

// InspectContainers returns inspect responses for multiple containers.
func (e *EngineClient) InspectContainers(ctx context.Context, ids ...string) ([]container.InspectResponse, error) {
	results := make([]container.InspectResponse, 0, len(ids))
	for _, id := range ids {
		res, err := e.API.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
		if err != nil {
			return nil, err
		}
		results = append(results, res.Container)
	}
	return results, nil
}

// InspectImage returns the full inspect response for an image.
func (e *EngineClient) InspectImage(ctx context.Context, ref string) (image.InspectResponse, error) {
	res, err := e.API.ImageInspect(ctx, ref)
	if err != nil {
		return image.InspectResponse{}, err
	}
	return res.InspectResponse, nil
}

// ListContainers returns container IDs matching the given label filters.
func (e *EngineClient) ListContainers(ctx context.Context, all bool, labelFilters []string) ([]string, error) {
	f := mobyclient.Filters{}
	for _, l := range labelFilters {
		f.Add("label", l)
	}
	res, err := e.API.ContainerList(ctx, mobyclient.ContainerListOptions{
		All:     all,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(res.Items))
	for i, c := range res.Items {
		ids[i] = c.ID
	}
	return ids, nil
}

// RemoveContainer force-removes a container, retrying up to 7 times when the
// removal is "already in progress" (mirrors the TS removeContainer logic).
func (e *EngineClient) RemoveContainer(ctx context.Context, id string) error {
	const maxRetries = 7
	const retryTimeout = time.Second

	var eventsCh <-chan events.Message
	var errCh <-chan error
	var cancelEvents context.CancelFunc

	defer func() {
		if cancelEvents != nil {
			cancelEvents()
		}
	}()

	// One timer reused across retries instead of a fresh time.After each
	// iteration (whose timers would linger until they fire).
	timer := time.NewTimer(retryTimeout)
	defer timer.Stop()

	for i := range maxRetries {
		_, err := e.API.ContainerRemove(ctx, id, mobyclient.ContainerRemoveOptions{Force: true})
		if err == nil {
			return nil
		}

		errMsg := strings.ToLower(err.Error())
		if i == maxRetries-1 || !strings.Contains(errMsg, "already in progress") {
			return err
		}

		// Start watching for the destroy event on first retry.
		if eventsCh == nil {
			evCtx, cancel := context.WithCancel(ctx)
			cancelEvents = cancel
			evRes := e.API.Events(evCtx, mobyclient.EventsListOptions{
				Filters: mobyclient.Filters{}.Add("container", id).Add("event", "destroy"),
			})
			eventsCh, errCh = evRes.Messages, evRes.Err
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(retryTimeout)
		select {
		case <-eventsCh:
			return nil // destroyed
		case err := <-errCh:
			return fmt.Errorf("events stream: %w", err)
		case <-timer.C:
			// retry
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Events streams Docker events filtered by the given map (e.g. {"container": ["id"], "event": ["destroy"]}).
// Cancel the context to stop streaming.
func (e *EngineClient) Events(ctx context.Context, eventFilters map[string][]string) (<-chan events.Message, <-chan error) {
	f := mobyclient.Filters{}
	for key, values := range eventFilters {
		f.Add(key, values...)
	}
	res := e.API.Events(ctx, mobyclient.EventsListOptions{Filters: f})
	return res.Messages, res.Err
}

// IsPodman returns true if the daemon identifies itself as Podman.
func (e *EngineClient) IsPodman(ctx context.Context) bool {
	ver, err := e.API.ServerVersion(ctx, mobyclient.ServerVersionOptions{})
	if err != nil {
		return false
	}
	for _, comp := range ver.Components {
		if strings.EqualFold(comp.Name, "podman") {
			return true
		}
	}
	return false
}

// PullImage pulls an image from a registry, consuming the output stream to
// completion. It uses no authentication by default (public images).
func (e *EngineClient) PullImage(ctx context.Context, ref string) error {
	return e.PullImagePlatform(ctx, ref, "")
}

// parsePlatform splits an "os/arch[/variant]" platform string into an OCI
// platform. Malformed input yields a best-effort value (the daemon rejects it).
func parsePlatform(s string) ocispec.Platform {
	parts := strings.SplitN(s, "/", 3)
	p := ocispec.Platform{OS: parts[0]}
	if len(parts) > 1 {
		p.Architecture = parts[1]
	}
	if len(parts) > 2 {
		p.Variant = parts[2]
	}
	return p
}

// PullImagePlatform pulls ref for a specific platform (e.g. "linux/amd64"), or
// the daemon default when platform is empty. Used so an image-based dev container
// is pulled for the platform it will actually run on (#1241).
func (e *EngineClient) PullImagePlatform(ctx context.Context, ref, platform string) error {
	opts := mobyclient.ImagePullOptions{}
	if platform != "" {
		opts.Platforms = []ocispec.Platform{parsePlatform(platform)}
	}
	reader, err := e.API.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer reader.Close()
	// Drain the pull output — the pull isn't complete until EOF.
	_, err = io.Copy(io.Discard, reader)
	return err
}

// StartContainer starts a stopped container.
func (e *EngineClient) StartContainer(ctx context.Context, id string) error {
	_, err := e.API.ContainerStart(ctx, id, mobyclient.ContainerStartOptions{})
	return err
}

// StopContainer gracefully stops a container (SIGTERM, then SIGKILL after the
// daemon's default grace period) without removing it, so it can be restarted
// with `up`. Stopping an already-stopped container is a no-op.
func (e *EngineClient) StopContainer(ctx context.Context, id string) error {
	_, err := e.API.ContainerStop(ctx, id, mobyclient.ContainerStopOptions{})
	return err
}

// CreateContainer creates a new container and returns its ID.
func (e *EngineClient) CreateContainer(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) (string, error) {
	res, err := e.API.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{Config: config, HostConfig: hostConfig})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// ImageTag tags an image.
func (e *EngineClient) ImageTag(ctx context.Context, source, target string) error {
	_, err := e.API.ImageTag(ctx, mobyclient.ImageTagOptions{Source: source, Target: target})
	return err
}

// --- Utility functions (no daemon required) ---

var (
	invalidImageChars  = regexp.MustCompile(`[^a-z0-9._-]+`)
	collapsiblePattern = regexp.MustCompile(`(\.[\._-]|_[\.-]|__[\._-]|-+[\._])[\._-]*`)
)

// ToImageName sanitises a string into a valid Docker image name component.
func ToImageName(name string) string {
	s := strings.ToLower(name)
	s = invalidImageChars.ReplaceAllString(s, "")
	s = collapsiblePattern.ReplaceAllStringFunc(s, func(m string) string {
		// Keep prefix minus its last character (the separator that started the collapsible run).
		match := collapsiblePattern.FindStringSubmatch(m)
		if len(match) < 2 {
			return m
		}
		return match[1][:len(match[1])-1]
	})
	return s
}
