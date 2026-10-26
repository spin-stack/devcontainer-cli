package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
)

// composeAutoAddedStageLabel is the stage name TS buildAndExtendDockerCompose
// gives the service's final stage (or an image-only service's FROM).
const composeAutoAddedStageLabel = "dev_container_auto_added_stage_label"

// composeSyntaxLineRe is TS's /^\s*#\s*syntax\s*=.*[\r\n]/ (start of file only).
var composeSyntaxLineRe = regexp.MustCompile(`^\s*#\s*syntax\s*=.*[\r\n]`)

// composeStart holds what startComposeContainer needs (TS startContainer).
type composeStart struct {
	cfg             *config.DevContainer
	loadResult      *config.LoadResult
	workspaceFolder string
	idLabels        []string
	useBuildx       bool
	projectName     string
	composeFiles    []string
	composeConfig   map[string]any
	client          *docker.ComposeClient
	// container is the existing (stopped) service container, nil for none.
	container *container.InspectResponse
	// The build command's settings (zero for up): metadataOnImage puts the
	// metadata label on an image-only service's image even without Features (TS
	// canAddLabelsToContainer false), buildServices are the services `compose
	// build` builds (nil for TS up's runServices filter), skipPersistCustoms
	// leaves the Features' customizations out of the metadata.
	metadataOnImage    bool
	buildServices      []string
	skipPersistCustoms bool
}

// persistedFolder is TS common.persistedFolder: --user-data-folder, or
// the CLI cache folder as a fallback.
func (r *upRunner) persistedFolder() string {
	if r.opts.userDataFolder != "" {
		return r.opts.userDataFolder
	}
	return cacheFolder()
}

// composeService returns the service's entry of the `docker compose config` output.
func composeService(composeConfig map[string]any, service string) (map[string]any, bool) {
	services, _ := composeConfig["services"].(map[string]any)
	svc, ok := services[service].(map[string]any)
	return svc, ok
}

// composeStringList reads a compose entrypoint/command: nil when unset, the list
// as is, or a string split like a shell would (TS shellQuote.parse).
func composeStringList(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		return splitShellWords(t)
	}
	return nil
}

// splitShellWords splits s into words honoring single/double quotes and
// backslash escapes, the subset of shell-quote parsing compose values need.
func splitShellWords(s string) []string {
	words := []string{}
	var cur strings.Builder
	inWord, quote := false, rune(0)
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// newRandomUUID returns a random (version 4) UUID string.
func newRandomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] &= 0x0f
	b[6] |= 0x40 // version 4
	b[8] &= 0x3f
	b[8] |= 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// persistedComposeFile is TS checkForPersistedFile's result.
type persistedComposeFile struct {
	foundLabel, fileExists bool
	file                   string
}

func (r *upRunner) checkPersistedComposeFile(files []string, prefix string) persistedComposeFile {
	for _, f := range files {
		if !strings.Contains(f, prefix) {
			continue
		}
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			r.log.Write(fmt.Sprintf("Restoring %s from persisted storage", f), log.LevelInfo)
			return persistedComposeFile{foundLabel: true, fileExists: true, file: f}
		}
		r.log.Write(fmt.Sprintf("Expected %s to exist, but it did not", f), log.LevelError)
		return persistedComposeFile{foundLabel: true, file: f}
	}
	r.log.Write(fmt.Sprintf("Expected to find a docker-compose file prefixed with %s, but did not.", prefix), log.LevelError)
	return persistedComposeFile{}
}

// startComposeContainer ports TS startContainer (dockerCompose.ts): restore the
// override files a stopped container was created with, or (re)generate the build
// and container overrides — always, with or without Features — then `compose up`.
func (r *upRunner) startComposeContainer(ctx context.Context, s composeStart) (string, error) {
	logger, opts := r.log, r.opts
	cfg := s.cfg
	files := slices.Clone(s.composeFiles)
	persisted := r.persistedFolder()

	svc, _ := composeService(s.composeConfig, cfg.Service)
	svcImage, _ := svc["image"].(string)
	svcUser, _ := svc["user"].(string)
	originalImageName := svcImage
	if originalImageName == "" {
		originalImageName = s.client.DefaultImageName(s.projectName, cfg.Service)
	}

	hasLabels := s.container != nil && s.container.Config != nil && s.container.Config.Labels != nil
	logger.Write(fmt.Sprintf("PersistedPath=%s, ContainerHasLabels=%t", persisted, hasLabels), log.LevelInfo)
	restored := false
	if hasLabels {
		configFiles, ok := s.container.Config.Labels["com.docker.compose.project.config_files"]
		logger.Write(fmt.Sprintf("Container was created with these config files: %s", configFiles), log.LevelInfo)
		var labelFiles []string
		if ok {
			labelFiles = strings.Split(configFiles, ",")
		}
		buildFile := r.checkPersistedComposeFile(labelFiles, composeBuildOverridePrefix)
		startFile := r.checkPersistedComposeFile(labelFiles, composeFeaturesOverridePrefix)
		if (buildFile.fileExists || !buildFile.foundLabel) && startFile.fileExists {
			restored = true
			if buildFile.fileExists {
				files = append(files, buildFile.file)
			}
			files = append(files, startFile.file)
		}
	}

	if s.container == nil || !restored {
		// With an existing container only recreate the override files; skip the build.
		noBuild := s.container != nil
		versionPrefix, err := readComposeVersionPrefix(s.composeFiles)
		if err != nil {
			return "", err
		}
		ext, err := r.buildAndExtendCompose(ctx, s, persisted, versionPrefix, noBuild)
		if err != nil {
			return "", err
		}
		files = append(files, ext.overrideFiles...)

		currentImageName := originalImageName
		if ext.overrideImageName != "" {
			currentImageName = ext.overrideImageName
		}
		var cached *image.InspectResponse
		imageDetails := func() (image.InspectResponse, error) {
			if cached != nil {
				return *cached, nil
			}
			info, err := r.inspectImagePulling(ctx, currentImageName)
			if err != nil {
				return info, err
			}
			cached = &info
			return info, nil
		}
		merged := imagemeta.MergeConfiguration(ext.imageMetadata)
		updatedImageName := currentImageName
		if !noBuild {
			if updatedImageName, err = r.updateRemoteUserUID(ctx, merged, currentImageName, imageDetails, svcUser, s.workspaceFolder, persisted); err != nil {
				return "", err
			}
		}

		labels := append(slices.Clone(s.idLabels), ext.labels...)
		overridePath, err := r.writeComposeFeaturesOverride(ctx, s, updatedImageName, currentImageName, merged, versionPrefix, imageDetails, svc, labels, persisted)
		if err != nil {
			return "", err
		}
		files = append(files, overridePath)
	}

	noRecreate := s.container != nil || opts.expectExistingContainer
	if err := s.client.Up(ctx, files, "", nil, s.projectName, composeServicesFilter(cfg), noRecreate); err != nil {
		return "", fmt.Errorf("compose up: %w", err)
	}

	ids, err := r.engine.ListContainers(ctx, true, []string{
		"com.docker.compose.project=" + s.projectName,
		"com.docker.compose.service=" + cfg.Service,
	})
	if err != nil || len(ids) == 0 {
		return "", fmt.Errorf("could not find container for service %q in project %q", cfg.Service, s.projectName)
	}
	return ids[0], nil
}

// composeServicesFilter is the services `compose build` and `compose up` are
// given, as in TS: none (the whole project) without runServices, otherwise
// runServices plus the dev container service.
func composeServicesFilter(cfg *config.DevContainer) []string {
	if len(cfg.RunServices) == 0 {
		return nil
	}
	services := slices.Clone(cfg.RunServices)
	if !slices.Contains(services, cfg.Service) {
		services = append(services, cfg.Service)
	}
	return services
}

// inspectImagePulling is TS inspectDockerImage(..., pullImageOnError=true).
func (r *upRunner) inspectImagePulling(ctx context.Context, ref string) (image.InspectResponse, error) {
	info, err := r.engine.InspectImage(ctx, ref)
	if err == nil {
		return info, nil
	}
	if pullErr := r.engine.PullImage(ctx, ref); pullErr != nil {
		return info, fmt.Errorf("inspect image %q: %w (pull: %v)", ref, err, pullErr)
	}
	return r.engine.InspectImage(ctx, ref)
}

// composeExtendResult is what TS buildAndExtendDockerCompose returns.
type composeExtendResult struct {
	imageMetadata     []imagemeta.Entry
	overrideFiles     []string
	overrideImageName string
	// labels are "key=value" container labels (the metadata label for an
	// image-only service without Features).
	labels []string
}

// composeFeatureSets fetches the configured Features the way
// extendImageWithFeatures does (lockfile pinning/writing, host substitutions),
// returning nil when none are configured. The returned folder (TS dstFolder)
// holds each Feature at its consecutiveId; it is kept because the compose build
// override references it (TS keeps its features folder too).
//
// mutate-lane: TestParityMatrix - up.compose-config-file-fspath-features-success
// processes a compose configuration's Features through it.
func (r *upRunner) composeFeatureSets(ctx context.Context, cfg *config.DevContainer, loadResult *config.LoadResult) ([]*features.Set, string, error) {
	opts := r.opts
	if len(cfg.Features) > 0 {
		r.lockfileRead = !opts.noLockfile
	}
	// A lockfile that cannot be read fails the compose flow as it fails TS's
	// (readLockfile rethrows), reported by up and build as their error result.
	var lockfile *features.Lockfile
	if cfg.ConfigFilePath != "" && !opts.noLockfile {
		var err error
		if lockfile, _, err = features.ReadLockfile(cfg.ConfigFilePath); err != nil {
			return nil, "", err
		}
	}
	res, err := fetchFeatureSetsWithOrder(ctx, r.log, cfg.Features, filepath.Dir(cfg.ConfigFilePath), opts.skipFeatureAutoMapping, lockfile, cfg.OverrideFeatureInstallOrder)
	if err != nil {
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	if err := substituteFeatureHostVars(res.FeatureSets, loadResult.HostSub); err != nil {
		return nil, "", err
	}
	// Written by default, as TS generateFeaturesConfig writes it unless
	// --no-lockfile is given.
	if !opts.noLockfile && cfg.ConfigFilePath != "" {
		lf := features.GenerateLockfile(&features.Config{FeatureSets: res.FeatureSets}, opts.lockfileExcludeIDs)
		if err := features.WriteLockfile(cfg.ConfigFilePath, lf, features.WriteOptions{Frozen: opts.experimentalFrozenLockfile}); err != nil {
			return nil, "", fmt.Errorf("lockfile: %w", err)
		}
	}
	return res.FeatureSets, res.TmpDir, nil
}

// dockerfileSupportsBuildContexts is TS supportsBuildContexts: truthy for a
// docker/dockerfile frontend >= 1.4 (or untagged/latest/labs) and for an
// unknown frontend.
func dockerfileSupportsBuildContexts(df *docker.Dockerfile) bool {
	if df == nil {
		return false
	}
	version := df.Preamble.Version
	if version == "" {
		return df.Preamble.Directives["syntax"] != ""
	}
	m := regexp.MustCompile(`^\d+(\.\d+){0,2}`).FindString(version)
	if m == "" {
		return true
	}
	parts := strings.Split(m, ".")
	major, _ := strconv.Atoi(parts[0])
	if major != 1 {
		return major > 1
	}
	if len(parts) == 1 {
		return true // "1" intersects >=1.4
	}
	minor, _ := strconv.Atoi(parts[1])
	return minor >= 4
}

// buildAndExtendCompose ports TS buildAndExtendDockerCompose: compute the
// service's image metadata, generate the Features (or metadata-only) Dockerfile
// and the build override, and run `docker compose build` unless noBuild.
func (r *upRunner) buildAndExtendCompose(ctx context.Context, s composeStart, persisted, versionPrefix string, noBuild bool) (*composeExtendResult, error) {
	logger, opts := r.log, r.opts
	cfg := s.cfg
	svcBuild := parseComposeServiceBuild(s.composeConfig, cfg.Service)
	svc, _ := composeService(s.composeConfig, cfg.Service)
	svcUser, _ := svc["user"].(string)

	baseName := composeAutoAddedStageLabel
	dockerfile := ""
	var parsed *docker.Dockerfile
	var baseInfo image.InspectResponse
	originalDockerfilePath := ""
	if svcBuild.HasBuild {
		buildContext := svcBuild.Context
		if buildContext == "" {
			buildContext = filepath.Dir(s.composeFiles[0])
		}
		dockerfilePath := svcBuild.Dockerfile
		if dockerfilePath == "" {
			dockerfilePath = "Dockerfile"
		}
		if !filepath.IsAbs(dockerfilePath) {
			dockerfilePath = filepath.Join(buildContext, dockerfilePath)
		}
		originalDockerfilePath = dockerfilePath
		raw, err := os.ReadFile(dockerfilePath)
		if err != nil {
			return nil, fmt.Errorf("read Dockerfile for compose service: %w", err)
		}
		dockerfile = string(raw)
		if svcBuild.Target != "" {
			baseName = svcBuild.Target
		} else {
			name, modified, err := docker.EnsureFinalStageName(dockerfile, baseName)
			if err != nil {
				return nil, err
			}
			baseName = name
			if modified != "" {
				dockerfile = modified
			}
		}
		parsed = docker.ExtractDockerfile(string(raw))
		if baseImage := docker.FindBaseImage(parsed, svcBuild.Args, svcBuild.Target, buildxPlatformArgs("")); baseImage != "" {
			info, err := r.inspectImagePulling(ctx, baseImage)
			if err != nil {
				return nil, err
			}
			baseInfo = info
		}
	} else {
		if svcBuild.Image == "" {
			return nil, fmt.Errorf("compose service %q has neither an image nor a build", cfg.Service)
		}
		info, err := r.inspectImagePulling(ctx, svcBuild.Image)
		if err != nil {
			return nil, err
		}
		baseInfo = info
	}
	imageUser := "root"
	var baseEntries []imagemeta.Entry
	if baseInfo.Config != nil {
		if baseInfo.Config.User != "" {
			imageUser = baseInfo.Config.User
		}
		baseEntries = imagemeta.ReadMetadataFromLabels(baseInfo.Config.Labels, logger)
	}

	featureSets, featuresFolder, err := r.composeFeatureSets(ctx, cfg, s.loadResult)
	if err != nil {
		return nil, err
	}

	// TS getDevcontainerMetadata: the label records the image's entries, the
	// Features' and the configuration's as written (labelMetadata); the merged
	// configuration reads them with their variables substituted (metadata).
	var featureEntries []imagemeta.Entry
	for _, fs := range featureSets {
		featureEntries = append(featureEntries, featureMetadataEntry(fs, s.skipPersistCustoms))
	}
	labelMetadata := slices.Concat(baseEntries, featureEntries, []imagemeta.Entry{r.configLabel})
	metadata := slices.Clone(baseEntries)
	if err := r.substitute(&metadata); err != nil {
		return nil, fmt.Errorf("substitute the image's metadata: %w", err)
	}
	metadata = slices.Concat(metadata, featureEntries, []imagemeta.Entry{configMetadataEntry(cfg, false)})

	result := &composeExtendResult{imageMetadata: metadata}
	userSyntax := ""
	if parsed != nil {
		userSyntax = parsed.Preamble.Directives["syntax"]
	}

	var info *imagemeta.ExtendImageBuildInfo
	dstFolder := featuresFolder
	switch {
	case len(featureSets) == 0 && !svcBuild.HasBuild && !s.metadataOnImage:
		// TS getExtendImageBuildInfo: no Features and an image (no Dockerfile) →
		// only the metadata label, added to the container.
		result.labels = []string{imagemeta.MetadataLabel + "=" + containerMetadataLabel(labelMetadata)}
	case len(featureSets) == 0:
		// TS getImageBuildOptions: extend the Dockerfile with the metadata label.
		syntax := userSyntax
		if opts.omitSyntaxDirective {
			syntax = ""
		}
		dstFolder = filepath.Join(cacheFolder(), "container-features", fmt.Sprintf("%s-%d", cliVersion(), time.Now().UnixMilli()))
		if err := os.MkdirAll(dstFolder, 0o755); err != nil {
			return nil, fmt.Errorf("create features folder: %w", err)
		}
		info = imagemeta.GenerateComposeLabelBuild(baseName, labelMetadata, syntax)
	default:
		// TS findContainerUsers over the image + Features + config metadata.
		containerUser, remoteUser := findContainerUsers(metadata, svcUser, imageUser)
		// TS: BuildKit build contexts need Compose >= 2.17.0 (the CLI variant is
		// always Docker: Podman is unsupported, see docs/DIVERGENCES.md).
		useContexts := s.useBuildx && s.client.SupportsAdditionalContexts()
		syntax := ""
		switch {
		case opts.omitSyntaxDirective:
		case engineAtLeast23(ctx, r.engine):
			syntax = userSyntax
		case useContexts && !dockerfileSupportsBuildContexts(parsed):
			syntax = "docker/dockerfile:1.4"
		default:
			syntax = userSyntax
		}
		if err := stageFeatureBuildFiles(featuresFolder, featureSets, containerUser, remoteUser); err != nil {
			return nil, err
		}
		info = imagemeta.GenerateFeaturesBuildOptions(imagemeta.FeaturesBuildParams{
			BaseName:            baseName,
			FeatureSets:         featureSets,
			Metadata:            labelMetadata,
			ContainerUser:       containerUser,
			RemoteUser:          remoteUser,
			ImageUser:           imageUser,
			UseBuildKitContexts: useContexts,
			Syntax:              syntax,
			ConfigContainerEnv:  cfg.ContainerEnv,
		})
		if !useContexts {
			if err := buildFeatureContentImage(ctx, r.docker, featuresFolder); err != nil {
				return nil, err
			}
		}
	}

	override := composeBuildOverride{VersionPrefix: versionPrefix, Service: cfg.Service, CacheFrom: opts.cacheFrom}
	if info != nil {
		// Avoid retagging a previously pulled image.
		if !svcBuild.HasBuild {
			result.overrideImageName = folderImageName(s.workspaceFolder)
			override.Image = result.overrideImageName
		}
		if dockerfile == "" {
			dockerfile = fmt.Sprintf("FROM %s AS %s\n", svcBuild.Image, baseName)
		}
		// The prefix may declare a '# syntax' line, so strip the service's own.
		if loc := composeSyntaxLineRe.FindStringIndex(dockerfile); loc != nil {
			dockerfile = dockerfile[loc[1]:]
		}
		final := info.DockerfilePrefixContent + dockerfile + "\n" + info.DockerfileContent
		finalPath := filepath.Join(dstFolder, "Dockerfile-with-features")
		if err := os.WriteFile(finalPath, []byte(final), 0o644); err != nil {
			return nil, fmt.Errorf("write compose Dockerfile: %w", err)
		}
		if originalDockerfilePath != "" {
			// Keep the service Dockerfile's sibling .dockerignore (upstream #969).
			copyDockerignore(originalDockerfilePath, finalPath)
		}
		override.Dockerfile = finalPath
		if svcBuild.Target != "" {
			override.Target = info.OverrideTarget
		}
		if !svcBuild.HasBuild {
			emptyDir := filepath.Join(persisted, "empty-folder")
			if err := os.MkdirAll(emptyDir, 0o755); err != nil {
				return nil, fmt.Errorf("create empty context folder: %w", err)
			}
			override.Context = emptyDir
		}
		override.BuildKitInlineCache = s.useBuildx
		for _, k := range sortedStringKeys(info.BuildArgs) {
			override.BuildArgs = append(override.BuildArgs, [2]string{k, info.BuildArgs[k]})
		}
		for _, k := range sortedStringKeys(info.BuildKitContexts) {
			v := info.BuildKitContexts[k]
			if v == "." {
				v = dstFolder
			}
			override.AdditionalContexts = append(override.AdditionalContexts, [2]string{k, v})
		}
	}

	if content := generateComposeBuildOverride(override); content != "" {
		dir := filepath.Join(persisted, "docker-compose")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create compose override folder: %w", err)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.yml", composeBuildOverridePrefix, time.Now().UnixMilli()))
		logger.Write("Docker Compose override file for building image:\n"+content, log.LevelInfo)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write compose build override: %w", err)
		}
		result.overrideFiles = append(result.overrideFiles, path)
	}

	if !noBuild {
		files := append(slices.Clone(s.composeFiles), result.overrideFiles...)
		services := s.buildServices
		if services == nil {
			services = composeServicesFilter(cfg)
		}
		if err := s.client.Build(ctx, files, "", []string{"--project-name", s.projectName}, services, opts.buildNoCache); err != nil {
			return nil, fmt.Errorf("An error occurred building the Docker Compose images: %w", err)
		}
	}
	return result, nil
}

// updateRemoteUserUID ports TS updateRemoteUserUID for a compose service
// (dockerCompose.ts startContainer → containerFeatures.ts): rebuild the image
// with the remote user's UID/GID set to the host user's.
func (r *upRunner) updateRemoteUserUID(ctx context.Context, merged *imagemeta.MergedConfig, imageName string, imageDetails func() (image.InspectResponse, error), svcUser, workspaceFolder, persisted string) (string, error) {
	opts := r.opts
	enabled := opts.updateRemoteUserUIDDefault == "on"
	if merged.UpdateRemoteUserUID != nil {
		enabled = *merged.UpdateRemoteUserUID
	}
	if opts.updateRemoteUserUIDDefault == "never" || !enabled || runtime.GOOS != "linux" {
		return imageName, nil
	}
	details, err := imageDetails()
	if err != nil {
		return "", err
	}
	imageUser := "root"
	if details.Config != nil && details.Config.User != "" {
		imageUser = details.Config.User
	}
	remoteUser := merged.RemoteUser
	if remoteUser == "" {
		remoteUser = svcUser
	}
	if remoteUser == "" {
		remoteUser = imageUser
	}
	if remoteUser == "root" || regexp.MustCompile(`^\d+$`).MatchString(remoteUser) {
		return imageName, nil
	}
	folderImage := folderImageName(workspaceFolder)
	fixedImageName := folderImage + "-uid"
	if strings.HasPrefix(imageName, folderImage) {
		fixedImageName = imageName + "-uid"
	}
	var platform []string
	for _, p := range []string{details.Os, details.Architecture, details.Variant} {
		if p != "" {
			platform = append(platform, p)
		}
	}
	emptyDir := filepath.Join(persisted, "empty-folder")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		return "", fmt.Errorf("create empty context folder: %w", err)
	}
	if err := docker.BuildUIDImage(ctx, r.docker, docker.UIDUpdate{
		BaseImage:  imageName,
		FixedImage: fixedImageName,
		RemoteUser: remoteUser,
		ImageUser:  imageUser,
		Platform:   strings.Join(platform, "/"),
		UID:        os.Getuid(),
		GID:        os.Getgid(),
		ContextDir: emptyDir,
	}); err != nil {
		return "", err
	}
	return fixedImageName, nil
}

// writeComposeFeaturesOverride ports TS writeFeaturesComposeOverrideFile: render
// the container override and persist it under <persisted>/docker-compose.
func (r *upRunner) writeComposeFeaturesOverride(ctx context.Context, s composeStart, updatedImageName, originalImageName string, merged *imagemeta.MergedConfig, versionPrefix string, imageDetails func() (image.InspectResponse, error), svc map[string]any, labels []string, persisted string) (string, error) {
	logger, opts, cfg := r.log, r.opts, s.cfg

	devcontainerID := config.ComputeDevContainerID(config.EnvMap(s.idLabels))
	resolve := func(v string) (string, error) {
		resolved, err := config.NewVariableResolver().Resolve(config.SubstitutionContext{DevContainerID: devcontainerID}, config.PhaseIdentity, v)
		if err != nil {
			return "", err
		}
		str, ok := resolved.(string)
		if !ok {
			return "", fmt.Errorf("identity substitution returned %T for string", resolved)
		}
		return str, nil
	}
	mountEntries := slices.Clone(merged.Mounts)
	for _, m := range opts.mounts {
		mountEntries = append(mountEntries, m)
	}
	mounts, err := composeMountsFromMetadata(mountEntries, resolve)
	if err != nil {
		return "", fmt.Errorf("resolve compose mounts: %w", err)
	}

	in := composeFeaturesOverride{
		VersionPrefix:     versionPrefix,
		Service:           cfg.Service,
		UpdatedImageName:  updatedImageName,
		OriginalImageName: originalImageName,
		Merged:            merged,
		ServiceEntrypoint: composeStringList(svc["entrypoint"]),
		ServiceCommand:    composeStringList(svc["command"]),
		Labels:            labels,
		Mounts:            mounts,
	}
	overrideCommand := merged.OverrideCommand != nil && *merged.OverrideCommand
	if !overrideCommand && (in.ServiceEntrypoint == nil || in.ServiceCommand == nil) {
		details, err := imageDetails()
		if err != nil {
			return "", err
		}
		if details.Config != nil {
			in.ImageEntrypoint = details.Config.Entrypoint
			in.ImageCmd = details.Config.Cmd
		}
	}
	if cfg.HostRequirements != nil && gpuRequested(cfg.HostRequirements.GPU) {
		in.GPU = checkGPUAvailability(ctx, opts.gpuAvailability, r.docker)
		if !in.GPU && strings.TrimSpace(string(cfg.HostRequirements.GPU)) != `"optional"` {
			logger.Write(`No GPU support found yet a GPU was required - consider marking it as "optional"`, log.LevelWarning)
		}
	}

	content := generateComposeFeaturesOverride(in)
	logger.Write("Docker Compose override file for creating container:\n"+content, log.LevelInfo)
	name := fmt.Sprintf("%s-%d-%s.yml", composeFeaturesOverridePrefix, time.Now().UnixMilli(), newRandomUUID())
	dir := filepath.Join(persisted, "docker-compose")
	logger.Write(fmt.Sprintf("Writing %s to %s", name, dir), log.LevelInfo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create compose override folder: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write compose features override: %w", err)
	}
	return path, nil
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
