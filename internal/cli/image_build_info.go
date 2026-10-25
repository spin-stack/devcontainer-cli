package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		dockerfilePath := cfg.Dockerfile()
		if !filepath.IsAbs(dockerfilePath) {
			dockerfilePath = filepath.Join(configDir, dockerfilePath)
		}
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
	r := newDockerfileResolver(df, buildArgs, globalBuildxPlatformArgs(targetPlatform(), targetPlatform()))
	var img *oci.ImageConfig
	if baseImage := r.findBaseImage(target); baseImage != "" {
		var err error
		if img, err = insp.inspect(ctx, baseImage); err != nil {
			return nil, err
		}
	}
	var baseEnv map[string]string
	if img != nil {
		baseEnv = config.EnvMap(img.Env)
	}
	user := r.findUserStatement(baseEnv, target)
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

// argumentExpression is TS dockerfileUtils.ts argumentExpression.
var argumentExpression = regexp.MustCompile(`\$\{?([a-zA-Z0-9_]+)(:(-|\+)([^}]+))?\}?`)

// dfScope is a TS Stage or the preamble ({ from?, instructions }).
type dfScope struct {
	from         *docker.From
	instructions []docker.Instruction
}

// dockerfileResolver ports the variable resolution of TS dockerfileUtils.ts
// (replaceVariables / findValue), which findBaseImage and findUserStatement use.
// docker.FindBaseImage expands only the declared global ARGs, so it misses the
// automatic platform ARGs that TS (and BuildKit) provide.
type dockerfileResolver struct {
	preamble  *dfScope
	stages    []*dfScope
	byLabel   map[string]*dfScope
	buildArgs map[string]string
	platform  map[string]string
}

func newDockerfileResolver(df *docker.Dockerfile, buildArgs, platformArgs map[string]string) *dockerfileResolver {
	r := &dockerfileResolver{
		preamble:  &dfScope{instructions: df.Preamble.Instructions},
		byLabel:   map[string]*dfScope{},
		buildArgs: buildArgs,
		platform:  platformArgs,
	}
	for i := range df.Stages {
		s := &dfScope{from: &df.Stages[i].From, instructions: df.Stages[i].Instructions}
		r.stages = append(r.stages, s)
		if s.from.Label != "" {
			r.byLabel[s.from.Label] = s
		}
	}
	return r
}

func (r *dockerfileResolver) startStage(target string) *dfScope {
	if target != "" {
		return r.byLabel[target]
	}
	if len(r.stages) == 0 {
		return nil
	}
	return r.stages[len(r.stages)-1]
}

// fromImage resolves a stage's FROM image in the global scope.
func (r *dockerfileResolver) fromImage(s *dfScope, baseEnv map[string]string) string {
	return r.replace(baseEnv, s.from.Image, r.preamble, len(r.preamble.instructions))
}

// findBaseImage is TS findBaseImage: follow FROM <stage> to the first image
// that is not a stage. The base image's env is not available in FROM.
func (r *dockerfileResolver) findBaseImage(target string) string {
	seen := map[*dfScope]bool{}
	for s := r.startStage(target); s != nil; {
		if seen[s] {
			return ""
		}
		seen[s] = true
		image := r.fromImage(s, nil)
		next, ok := r.byLabel[image]
		if !ok {
			return image
		}
		s = next
	}
	return ""
}

// findUserStatement is TS findUserStatement: the last USER of the target stage
// (or of the stage it is built FROM), with its variables resolved.
func (r *dockerfileResolver) findUserStatement(baseEnv map[string]string, target string) string {
	seen := map[*dfScope]bool{}
	for s := r.startStage(target); s != nil; {
		if seen[s] {
			return ""
		}
		seen[s] = true
		for i := len(s.instructions) - 1; i >= 0; i-- {
			if s.instructions[i].Instruction == "USER" {
				return r.replace(baseEnv, s.instructions[i].Name, s, i)
			}
		}
		s = r.byLabel[r.fromImage(s, baseEnv)]
	}
	return ""
}

// replace is TS replaceVariables.
func (r *dockerfileResolver) replace(baseEnv map[string]string, str string, s *dfScope, before int) string {
	var b strings.Builder
	last := 0
	for _, m := range argumentExpression.FindAllStringSubmatchIndex(str, -1) { // mutate-exempt: any n < 0 is every match
		b.WriteString(str[last:m[0]])
		last = m[1]
		value := r.findValue(baseEnv, str[m[2]:m[3]], s, before)
		varExp := m[4]    // mutate-exempt: m[4] and m[5] are both -1 exactly when the :-word/:+word group is unmatched
		if varExp != -1 { // ${var:-word} / ${var:+word}
			option, word := str[m[6]], str[m[8]:m[9]] // the option is one character, - or +
			isSet := value != ""
			if (option == '-' && !isSet) || (option == '+' && isSet) {
				value = word
			}
			value = trimOneQuote(value)
		}
		b.WriteString(value)
	}
	b.WriteString(str[last:])
	return b.String()
}

// trimOneQuote strips one leading and one trailing quote, as TS getExpressionValue does.
func trimOneQuote(s string) string {
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	if s != "" && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

// findValue is TS findValue. An ARG without a default (and no build arg) does
// not define the variable, so the lookup continues outward.
func (r *dockerfileResolver) findValue(baseEnv map[string]string, variable string, s *dfScope, before int) string {
	considerArg := true
	seen := map[*dfScope]bool{}
	for {
		if seen[s] {
			return ""
		}
		seen[s] = true
		for i := min(before, len(s.instructions)) - 1; i >= 0; i-- {
			ins := s.instructions[i]
			if ins.Name != variable {
				continue
			}
			if ins.Instruction == "ENV" {
				return r.replace(baseEnv, ins.Value, s, i)
			}
			if ins.Instruction == "ARG" && considerArg {
				if v, ok := r.buildArgs[ins.Name]; ok {
					return r.replace(baseEnv, v, s, i)
				}
				if ins.Value != "" {
					return r.replace(baseEnv, ins.Value, s, i)
				}
			}
		}
		if s.from == nil {
			if v, ok := baseEnv[variable]; ok {
				return v
			}
			return r.platform[variable]
		}
		image := r.fromImage(s, baseEnv)
		next, ok := r.byLabel[image]
		if !ok {
			next = r.preamble
		}
		s = next
		before = len(s.instructions)
		considerArg = s == r.preamble
	}
}
