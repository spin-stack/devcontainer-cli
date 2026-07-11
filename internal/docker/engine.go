package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/core/log"
)

// DockerAPI is the subset of the Docker client API that EngineClient needs.
// Defined as an interface so it can be replaced in tests.
type DockerAPI interface {
	io.Closer
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	ImageInspect(ctx context.Context, imageID string, options ...dockerclient.ImageInspectOption) (image.InspectResponse, error)
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
	ImageTag(ctx context.Context, source, target string) error
	Ping(ctx context.Context) (types.Ping, error)
	ServerVersion(ctx context.Context) (types.Version, error)
}

// EngineClient talks to the Docker Engine via the SDK instead of shelling out.
type EngineClient struct {
	API DockerAPI
	Log log.Log
}

// NewEngineClient connects to the Docker daemon using environment defaults
// (DOCKER_HOST, DOCKER_TLS_VERIFY, etc.) with automatic API version negotiation.
// When DOCKER_HOST is not set, it resolves the active Docker context endpoint
// to match the behavior of the Docker CLI.
func NewEngineClient(logger log.Log, extraOpts ...dockerclient.Opt) (*EngineClient, error) {
	opts := []dockerclient.Opt{
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	}

	// When DOCKER_HOST is not set, resolve from the active Docker context
	// so the SDK talks to the same daemon as the Docker CLI.
	if os.Getenv("DOCKER_HOST") == "" {
		if host := resolveDockerContextHost(); host != "" {
			opts = append(opts, dockerclient.WithHost(host))
		}
	}

	opts = append(opts, extraOpts...)

	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker engine client: %w", err)
	}
	return &EngineClient{API: cli, Log: logger}, nil
}

// resolveDockerContextHost runs `docker context inspect` to get the endpoint
// of the currently active Docker context.
func resolveDockerContextHost() string {
	cmd := exec.Command("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Close releases the underlying connection.
func (e *EngineClient) Close() error {
	return e.API.Close()
}

// InspectContainer returns the full inspect response for one container.
func (e *EngineClient) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	return e.API.ContainerInspect(ctx, id)
}

// InspectContainers returns inspect responses for multiple containers.
func (e *EngineClient) InspectContainers(ctx context.Context, ids ...string) ([]container.InspectResponse, error) {
	results := make([]container.InspectResponse, 0, len(ids))
	for _, id := range ids {
		resp, err := e.API.ContainerInspect(ctx, id)
		if err != nil {
			return nil, err
		}
		results = append(results, resp)
	}
	return results, nil
}

// InspectImage returns the full inspect response for an image.
func (e *EngineClient) InspectImage(ctx context.Context, ref string) (image.InspectResponse, error) {
	return e.API.ImageInspect(ctx, ref)
}

// ListContainers returns container IDs matching the given label filters.
func (e *EngineClient) ListContainers(ctx context.Context, all bool, labelFilters []string) ([]string, error) {
	f := filters.NewArgs()
	for _, l := range labelFilters {
		f.Add("label", l)
	}
	containers, err := e.API.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(containers))
	for i, c := range containers {
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

	for i := range maxRetries {
		err := e.API.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
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
			eventsCh, errCh = e.API.Events(evCtx, events.ListOptions{
				Filters: filters.NewArgs(
					filters.Arg("container", id),
					filters.Arg("event", "destroy"),
				),
			})
		}

		select {
		case <-eventsCh:
			return nil // destroyed
		case err := <-errCh:
			return fmt.Errorf("events stream: %w", err)
		case <-time.After(retryTimeout):
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
	f := filters.NewArgs()
	for key, values := range eventFilters {
		for _, v := range values {
			f.Add(key, v)
		}
	}
	return e.API.Events(ctx, events.ListOptions{Filters: f})
}

// IsPodman returns true if the daemon identifies itself as Podman.
func (e *EngineClient) IsPodman(ctx context.Context) bool {
	ver, err := e.API.ServerVersion(ctx)
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
	reader, err := e.API.ImagePull(ctx, ref, image.PullOptions{})
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
	return e.API.ContainerStart(ctx, id, container.StartOptions{})
}

// CreateContainer creates a new container and returns its ID.
func (e *EngineClient) CreateContainer(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) (string, error) {
	resp, err := e.API.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ImageTag tags an image.
func (e *EngineClient) ImageTag(ctx context.Context, source, target string) error {
	return e.API.ImageTag(ctx, source, target)
}

// --- Utility functions (no daemon required) ---

var (
	invalidImageChars  = regexp.MustCompile(`[^a-z0-9._-]+`)
	collapsiblePattern = regexp.MustCompile(`(\.[\._-]|_[\.-]|__[\._-]|-+[\._])[\._-]*`)
)

// ToDockerImageName sanitises a string into a valid Docker image name component.
func ToDockerImageName(name string) string {
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
