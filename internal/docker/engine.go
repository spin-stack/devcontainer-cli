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
	ServerVersion(ctx context.Context, options mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error)
}

type EngineClient struct {
	API API
	Log log.Logger
}

func NewEngineClient(logger log.Logger, extraOpts ...mobyclient.Opt) (*EngineClient, error) {
	// API-version negotiation is enabled by default in the v29 client.
	opts := []mobyclient.Opt{
		mobyclient.FromEnv,
	}

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

func (e *EngineClient) Close() error {
	return e.API.Close()
}

func (e *EngineClient) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := e.API.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	return res.Container, nil
}

func (e *EngineClient) InspectImage(ctx context.Context, ref string) (image.InspectResponse, error) {
	res, err := e.API.ImageInspect(ctx, ref)
	if err != nil {
		return image.InspectResponse{}, err
	}
	return res.InspectResponse, nil
}

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
			return nil
		case err := <-errCh:
			return fmt.Errorf("events stream: %w", err)
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (e *EngineClient) PullImage(ctx context.Context, ref string) error {
	return e.PullImagePlatform(ctx, ref, "")
}

func (e *EngineClient) ImageLabelsEnsuringPresent(ctx context.Context, ref string) map[string]string {
	if info, err := e.InspectImage(ctx, ref); err == nil && info.Config != nil {
		return info.Config.Labels
	}
	if err := e.PullImage(ctx, ref); err != nil {
		return nil
	}
	if info, err := e.InspectImage(ctx, ref); err == nil && info.Config != nil {
		return info.Config.Labels
	}
	return nil
}

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

func (e *EngineClient) StartContainer(ctx context.Context, id string) error {
	_, err := e.API.ContainerStart(ctx, id, mobyclient.ContainerStartOptions{})
	return err
}

func (e *EngineClient) StopContainer(ctx context.Context, id string) error {
	_, err := e.API.ContainerStop(ctx, id, mobyclient.ContainerStopOptions{})
	return err
}

func (e *EngineClient) StopContainerNow(ctx context.Context, id string) error {
	_, err := e.API.ContainerStop(ctx, id, mobyclient.ContainerStopOptions{Timeout: new(0)})
	return err
}

func (e *EngineClient) CreateContainer(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) (string, error) {
	res, err := e.API.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{Config: config, HostConfig: hostConfig})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

var (
	invalidImageChars  = regexp.MustCompile(`[^a-z0-9._-]+`)
	collapsiblePattern = regexp.MustCompile(`(\.[\._-]|_[\.-]|__[\._-]|-+[\._])[\._-]*`)
)

func ToImageName(name string) string {
	s := strings.ToLower(name)
	s = invalidImageChars.ReplaceAllString(s, "")
	s = collapsiblePattern.ReplaceAllStringFunc(s, func(m string) string {
		match := collapsiblePattern.FindStringSubmatch(m)
		if len(match) < 2 {
			return m
		}
		return match[1][:len(match[1])-1]
	})
	return s
}
