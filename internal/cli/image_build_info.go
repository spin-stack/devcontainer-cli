package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// imageBuildInfo is TS ImageBuildInfo (imageMetadata.ts): the user the image
// runs as and the devcontainer.metadata entries of the image a configuration
// starts from (the base image for a Dockerfile).
type imageBuildInfo struct {
	User     string
	Metadata []imagemeta.Entry
}

// errDockerEngineUnavailable is the local-inspect failure when no Docker engine
// client could be created.
var errDockerEngineUnavailable = errors.New("docker engine unavailable")

// imageInspector is TS inspectDockerImage(params, name, pullImageOnError=true)
// (utils.ts): inspect the local image; failing that, read its config from the
// registry; failing that, pull it (with retries) and inspect it again.
type imageInspector struct {
	log      log.Logger
	local    func(ctx context.Context, name string) (*oci.ImageConfig, error)
	registry func(ctx context.Context, name string) (*oci.ImageConfig, error)
	pull     func(ctx context.Context, name string) error
	// pullAttempts and pullInterval are TS retry({ maxRetries: 5,
	// retryIntervalMilliseconds: 1000 }).
	pullAttempts int
	pullInterval time.Duration
}

// targetPlatform is the platform read-configuration inspects images for: TS
// uses the host's (mapNodeOSToGOOS / mapNodeArchitectureToGOARCH), no variant.
func targetPlatform() ocispec.Platform {
	return ocispec.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}
}

// newImageInspector wires the inspector to the Docker engine (local inspect and
// pull) and to the invocation's OCI client (registry inspect).
func newImageInspector(ctx context.Context, lgr log.Logger, engine *docker.EngineClient) *imageInspector {
	return &imageInspector{
		log: lgr,
		local: func(ctx context.Context, name string) (*oci.ImageConfig, error) {
			if engine == nil {
				return nil, errDockerEngineUnavailable
			}
			info, err := engine.InspectImage(ctx, name)
			if err != nil {
				return nil, err
			}
			cfg := &oci.ImageConfig{}
			if info.Config != nil {
				cfg.User, cfg.Env, cfg.Labels = info.Config.User, info.Config.Env, info.Config.Labels
			}
			return cfg, nil
		},
		registry: func(ctx context.Context, name string) (*oci.ImageConfig, error) {
			details, err := newOCIClient(ctx, lgr).InspectImageInRegistry(ctx, name, targetPlatform())
			if err != nil {
				return nil, err
			}
			return &details.Config, nil
		},
		pull: func(ctx context.Context, name string) error {
			if engine == nil {
				return errDockerEngineUnavailable
			}
			return engine.PullImage(ctx, name)
		},
		pullAttempts: 5,
		pullInterval: time.Second,
	}
}

func (i *imageInspector) inspect(ctx context.Context, name string) (*oci.ImageConfig, error) {
	cfg, inspectErr := i.local(ctx, name)
	if inspectErr == nil {
		return cfg, nil
	}
	cfg, err := i.registry(ctx, name)
	if err == nil {
		return cfg, nil
	}
	i.log.Write(fmt.Sprintf("Error fetching image details: %v", err), log.LevelInfo)
	var pullErr error
	for attempt := range i.pullAttempts {
		if pullErr = i.pull(ctx, name); pullErr == nil {
			break
		}
		i.log.Write(fmt.Sprintf("Retrying (Attempt %d) with error '%v'", attempt, pullErr), log.LevelWarning)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(i.pullInterval):
		}
	}
	if pullErr != nil {
		i.log.Write(inspectErr.Error(), log.LevelError)
		return nil, fmt.Errorf("pull image %s: %w", name, pullErr)
	}
	cfg, err = i.local(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspect image %s: %w", name, err)
	}
	return cfg, nil
}

// composeConfigFunc reads `docker compose config` for the given files.
type composeConfigFunc func(ctx context.Context, composeFiles []string, envFile string) (map[string]any, error)

// getImageBuildInfo is TS getImageBuildInfo (imageMetadata.ts:333).
func getImageBuildInfo(ctx context.Context, cfg *config.DevContainer, insp *imageInspector, composeConfig composeConfigFunc, lgr log.Logger) (*imageBuildInfo, error) {
	configDir := filepath.Dir(cfg.ConfigFilePath)
	switch {
	case cfg.IsDockerfileConfig():
		dockerfilePath := dockerfileConfigPath(cfg)
		if st, err := os.Stat(dockerfilePath); err != nil || st.IsDir() {
			return nil, fmt.Errorf("Dockerfile (%s) not found.", dockerfilePath)
		}
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			return nil, fmt.Errorf("read Dockerfile: %w", err)
		}
		target := ""
		if cfg.Build != nil {
			target = cfg.Build.Target
		}
		return imageBuildInfoFromDockerfile(ctx, insp, string(content), buildArgsFromConfig(cfg), target, lgr)

	case cfg.IsComposeConfig():
		composeFiles, err := config.DockerComposeFilePaths(cfg, osEnvMap(), configDir)
		if err != nil {
			return nil, fmt.Errorf("resolve compose files: %w", err)
		}
		composeCfg, err := composeConfig(ctx, composeFiles, "")
		if err != nil {
			return nil, err
		}
		if _, ok := composeService(composeCfg, cfg.Service); !ok {
			return nil, fmt.Errorf("Service '%s' configured in devcontainer.json not found in Docker Compose configuration.", cfg.Service)
		}
		svc := parseComposeServiceBuild(composeCfg, cfg.Service)
		if svc.HasBuild {
			buildContext := svc.Context
			if buildContext == "" {
				buildContext = filepath.Dir(composeFiles[0])
			}
			dockerfilePath := svc.Dockerfile
			if dockerfilePath == "" {
				dockerfilePath = "Dockerfile"
			}
			if !filepath.IsAbs(dockerfilePath) {
				dockerfilePath = filepath.Join(buildContext, dockerfilePath)
			}
			content, err := os.ReadFile(dockerfilePath)
			if err != nil {
				return nil, fmt.Errorf("read Dockerfile for compose service %q: %w", cfg.Service, err)
			}
			return imageBuildInfoFromDockerfile(ctx, insp, string(content), svc.Args, svc.Target, lgr)
		}
		return imageBuildInfoFromImage(ctx, insp, svc.Image, lgr)

	default:
		if cfg.Image == "" {
			return nil, errors.New("No image information specified in devcontainer.json.")
		}
		return imageBuildInfoFromImage(ctx, insp, cfg.Image, lgr)
	}
}

// imageBuildInfoFromImage is TS getImageBuildInfoFromImage.
func imageBuildInfoFromImage(ctx context.Context, insp *imageInspector, name string, lgr log.Logger) (*imageBuildInfo, error) {
	img, err := insp.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	user := img.User
	if user == "" {
		user = "root"
	}
	return &imageBuildInfo{User: user, Metadata: imagemeta.ReadMetadataFromLabels(img.Labels, lgr)}, nil
}

// imageBuildInfoFromDockerfile is TS internalGetImageBuildInfoFromDockerfile:
// the base image (resolved with the build args, the target and the automatic
// BUILD*/TARGET* platform args) is inspected for its metadata and user; a USER
// statement in the Dockerfile takes precedence over the image's user.
func imageBuildInfoFromDockerfile(ctx context.Context, insp *imageInspector, dockerfileText string, buildArgs map[string]string, target string, lgr log.Logger) (*imageBuildInfo, error) {
	df := docker.ExtractDockerfile(dockerfileText)
	r := docker.NewResolver(df, buildArgs, globalBuildxPlatformArgs(targetPlatform(), targetPlatform()))
	var img *oci.ImageConfig
	if baseImage := r.FindBaseImage(target); baseImage != "" {
		var err error
		if img, err = insp.inspect(ctx, baseImage); err != nil {
			return nil, err
		}
	}
	var baseEnv map[string]string
	if img != nil {
		baseEnv = config.EnvMap(img.Env)
	}
	user := r.FindUserStatement(baseEnv, target)
	if user == "" && img != nil {
		user = img.User
	}
	if user == "" {
		user = "root"
	}
	info := &imageBuildInfo{User: user}
	if img != nil {
		info.Metadata = imagemeta.ReadMetadataFromLabels(img.Labels, lgr)
	}
	return info, nil
}

// buildxPlatformArgs are the automatic platform ARGs of a build on this host
// for platform, os/arch[/variant] as --platform takes it, the host's when
// empty (TS devContainers.ts targetPlatformInfo).
func buildxPlatformArgs(platform string) map[string]string {
	target := targetPlatform()
	if platform != "" {
		os, rest, _ := strings.Cut(platform, "/")
		arch, variant, _ := strings.Cut(rest, "/")
		target = ocispec.Platform{OS: os, Architecture: arch, Variant: variant}
	}
	return globalBuildxPlatformArgs(targetPlatform(), target)
}

// globalBuildxPlatformArgs are BuildKit's automatic platform ARGs in the global
// scope, as TS internalGetImageBuildInfoFromDockerfile defines them.
func globalBuildxPlatformArgs(build, target ocispec.Platform) map[string]string {
	join := func(p ocispec.Platform) string {
		return strings.Join(slices.DeleteFunc([]string{p.OS, p.Architecture, p.Variant}, func(s string) bool { return s == "" }), "/")
	}
	return map[string]string{
		"BUILDPLATFORM":  join(build),
		"BUILDOS":        build.OS,
		"BUILDARCH":      build.Architecture,
		"BUILDVARIANT":   build.Variant,
		"TARGETPLATFORM": join(target),
		"TARGETOS":       target.OS,
		"TARGETARCH":     target.Architecture,
		"TARGETVARIANT":  target.Variant,
	}
}
