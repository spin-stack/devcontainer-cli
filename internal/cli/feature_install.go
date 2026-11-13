package cli

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/httpx"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	mobyclient "github.com/moby/moby/client"
)

type FeatureBuildOptions struct {
	NoCache                     bool
	HostSub                     *config.HostSubContext
	CacheFrom                   []string
	CacheTo                     string
	Labels                      []string
	Platform                    string
	Push                        bool
	Output                      string
	Secrets                     []string
	CABundle                    string
	ContainerEnv                map[string]string
	SkipPersistCustoms          bool
	FeaturesBasePath            string
	WorkspaceFolder             string
	AdditionalMetadata          []imagemeta.Entry
	LabelMetadata               []imagemeta.Entry
	FrozenLockfile              bool
	NoLockfile                  bool
	ConfigPath                  string
	LockfileExcludeIDs          map[string]bool
	OverrideFeatureInstallOrder []string
	OmitSyntaxDirective         bool
}

type fetchFeatureResult struct {
	FeatureSets []*features.Set
	TmpDir      string
}

func fetchFeatureSets(ctx context.Context, logger log.Logger, featuresCfg map[string]any, local localFeatureRoots) (*fetchFeatureResult, error) {
	return fetchFeatureSetsWithOrder(ctx, logger, featuresCfg, local, nil, nil)
}

func sortedUserFeatures(featuresCfg map[string]any) []features.DevContainerFeature {
	userFeatures := features.UserFeaturesToArray(featuresCfg)
	sort.Slice(userFeatures, func(i, j int) bool {
		return userFeatures[i].UserFeatureID < userFeatures[j].UserFeatureID
	})
	return userFeatures
}

type localFeatureRoots struct {
	configDir     string
	workspaceRoot string
}

func (r localFeatureRoots) resolve(logger log.Logger, id string) (string, bool, error) {
	path, err := features.LocalFeaturePath(id, r.configDir, r.workspaceRoot)
	if invalid, ok := errors.AsType[*features.InvalidLocalFeatureError](err); ok {
		logger.Write(invalid.Msg, log.LevelError)
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, true, nil
}

func createFeaturesFolder() (string, error) {
	parent := filepath.Join(cacheFolder(), "container-features")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create features folder: %w", err)
	}
	for ms := time.Now().UnixMilli(); ; ms++ {
		dir := filepath.Join(parent, fmt.Sprintf("%s-%d", cliVersion(), ms))
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create features folder: %w", err)
		}
	}
}

func fetchFeatureSetsWithOrder(ctx context.Context, logger log.Logger, featuresCfg map[string]any, local localFeatureRoots, lockfile *features.Lockfile, overrideOrder []string) (*fetchFeatureResult, error) {
	if len(featuresCfg) == 0 {
		return nil, nil
	}

	userFeatures := sortedUserFeatures(featuresCfg)
	userFeatures, warnings := features.UpdateDeprecatedFeaturesIntoOptions(userFeatures)
	for _, w := range warnings {
		logger.Write(w, log.LevelWarning)
	}

	logger.Write(fmt.Sprintf("Installing %d feature(s)...", len(userFeatures)), log.LevelInfo)

	ociClient := newOCIClient(ctx, logger)

	dstFolder, err := createFeaturesFolder()
	if err != nil {
		return nil, err
	}

	var stageIdx int
	nextStageDir := func() string {
		dir := filepath.Join(dstFolder, fmt.Sprintf("%s%d", featureStagePrefix, stageIdx))
		stageIdx++
		return dir
	}

	processFeature := func(node *features.FNode) (*features.Set, error) {
		return processInstallFeature(logger, ociClient, node, local, lockfile, nextStageDir)
	}

	graph, err := features.BuildDependencyGraph(logger, processFeature, userFeatures, overrideOrder, lockfile)
	if err != nil {
		os.RemoveAll(dstFolder)
		return nil, err
	}
	if len(graph) == 0 {
		os.RemoveAll(dstFolder)
		return nil, nil
	}

	orderedSets, err := features.ComputeInstallationOrder(logger, graph)
	if err != nil {
		os.RemoveAll(dstFolder)
		return nil, fmt.Errorf("compute Feature install order: %w", err)
	}

	if err := realignFeatureDirs(dstFolder, orderedSets); err != nil {
		os.RemoveAll(dstFolder)
		return nil, err
	}

	logger.Write(fmt.Sprintf("Install order: %s", featureSetIDs(orderedSets)), log.LevelTrace)
	logFeatureAdvisories(ctx, logger, orderedSets)
	return &fetchFeatureResult{FeatureSets: orderedSets, TmpDir: dstFolder}, nil
}

const featureStagePrefix = ".stage-"

func featureFromMetadata(m features.Feature, id string, opts any, cachePath string) features.Feature {
	return features.Feature{
		ID:                   id,
		Version:              m.Version,
		Name:                 m.Name,
		Description:          m.Description,
		DocumentationURL:     m.DocumentationURL,
		Value:                opts,
		UserOptions:          extractUserOptions(opts),
		Options:              m.Options,
		DependsOn:            m.DependsOn,
		DependsOnOrder:       m.DependsOnOrder,
		InstallsAfter:        m.InstallsAfter,
		LegacyIds:            m.LegacyIds,
		ContainerEnv:         m.ContainerEnv,
		Mounts:               m.Mounts,
		Init:                 m.Init,
		Privileged:           m.Privileged,
		CapAdd:               m.CapAdd,
		SecurityOpt:          m.SecurityOpt,
		Entrypoint:           m.Entrypoint,
		OnCreateCommand:      m.OnCreateCommand,
		UpdateContentCommand: m.UpdateContentCommand,
		PostCreateCommand:    m.PostCreateCommand,
		PostStartCommand:     m.PostStartCommand,
		PostAttachCommand:    m.PostAttachCommand,
		Customizations:       m.Customizations,
		Included:             true,
		CachePath:            cachePath,
		Metadata:             m.Metadata,
		SourceKeys:           features.LocalFeatureKeys,
	}
}

func processInstallFeature(
	logger log.Logger,
	ociClient oci.Registry,
	node *features.FNode,
	local localFeatureRoots,
	lockfile *features.Lockfile,
	nextStageDir func() string,
) (*features.Set, error) {
	id := node.UserFeatureID
	opts := node.Options
	resolvedID, srcType, err := resolveFeatureID(logger, id)
	if err != nil {
		return nil, err
	}

	switch srcType {
	case features.SourceLocalPath:
		resolvedPath, ok, err := local.resolve(logger, id)
		if !ok {
			return nil, err
		}

		info, statErr := os.Stat(resolvedPath)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  Local Feature path '%s' not found.", id, resolvedPath)
		}

		featureDir := nextStageDir()
		if err := copyDir(resolvedPath, featureDir); err != nil {
			return nil, fmt.Errorf("copy local feature %q: %w", id, err)
		}

		var m features.Feature
		metaData, readErr := os.ReadFile(filepath.Join(featureDir, "devcontainer-feature.json"))
		if readErr != nil {
			return nil, fmt.Errorf("read local feature %q metadata: %w", id, readErr)
		}
		if err := jsonc.Unmarshal(metaData, &m); err != nil {
			return nil, fmt.Errorf("parse local feature %q metadata: %w", id, err)
		}

		featID := m.ID
		if featID == "" {
			featID = filepath.Base(id)
		}
		feat := featureFromMetadata(m, featID, opts, featureDir)
		if feat.Name == "" {
			feat.Name = id
		}
		return &features.Set{
			SourceInfo:      &features.LocalSource{LocalPath: id, ResolvedPath: resolvedPath, UserID: id},
			Features:        []features.Feature{feat},
			InternalVersion: "2",
		}, nil

	case features.SourceDirectTarball:
		featureDir := nextStageDir()
		os.MkdirAll(featureDir, 0o755)

		logger.Write(fmt.Sprintf("Fetching feature tarball %s...", id), log.LevelInfo)
		blobData, computedDigest, dlErr := downloadFeatureTarball(context.Background(), logger, id, osEnvMap())
		if dlErr != nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, dlErr)
		}
		if err := verifyTarballDigest(lockfile, id, computedDigest); err != nil {
			return nil, err
		}
		tgzPath := filepath.Join(featureDir, "feature.tgz")
		if err := os.WriteFile(tgzPath, blobData, 0o644); err != nil {
			return nil, fmt.Errorf("write feature tarball: %w", err)
		}
		if err := extractTarGz(tgzPath, featureDir); err != nil {
			return nil, fmt.Errorf("extract feature %q: %w", id, err)
		}

		var m features.Feature
		if metaData, readErr := os.ReadFile(filepath.Join(featureDir, "devcontainer-feature.json")); readErr == nil {
			json.Unmarshal(metaData, &m)
		}
		featID := m.ID
		if featID == "" {
			featID = tarballFeatureID(id)
		}

		feat := featureFromMetadata(m, featID, opts, featureDir)
		if feat.Name == "" {
			feat.Name = id
		}
		return &features.Set{
			SourceInfo:      &features.TarballSource{TarballURI: id, UserID: id},
			Features:        []features.Feature{feat},
			InternalVersion: "2",
			ComputedDigest:  computedDigest,
		}, nil

	default:
		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
		}

		userRef := ref
		if lockfile != nil {
			if integrity := lockfile.Features[resolvedID].Integrity; integrity != "" {
				if pinned, perr := oci.ParseRef(ref.Resource + "@" + integrity); perr == nil {
					ref = pinned
				}
			}
		}

		logger.Write(fmt.Sprintf("Fetching feature %s...", ref.Resource), log.LevelInfo)

		manifest, err := ociClient.FetchManifest(ref, "")
		if err != nil {
			// TS logs this before its GitHub Releases fallback, which this CLI lacks.
			logger.Write(fmt.Sprintf("Could not resolve Feature manifest for '%s'.  If necessary, provide registry credentials with 'docker login <registry>'.", resolvedID), log.LevelWarning)
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  You may not have permission to access this Feature, or may not be logged in.  If the issue persists, report this to the Feature author.", id)
		}
		if len(manifest.Manifest.Layers) == 0 {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  The Feature manifest has no layers.", id)
		}

		layer := manifest.Manifest.Layers[0]
		blobData, err := ociClient.FetchBlob(ref, layer.Digest)
		if err != nil {
			return nil, fmt.Errorf("fetch feature %q blob: %w", id, err)
		}

		featureDir := nextStageDir()
		os.MkdirAll(featureDir, 0o755)
		tgzPath := filepath.Join(featureDir, "feature.tgz")
		if err := os.WriteFile(tgzPath, blobData, 0o644); err != nil {
			return nil, fmt.Errorf("write feature tarball: %w", err)
		}
		if err := extractTarGz(tgzPath, featureDir); err != nil {
			return nil, fmt.Errorf("extract feature %q: %w", id, err)
		}
		logger.Write(fmt.Sprintf("Extracted feature %s (%d bytes)", ref.ID, len(blobData)), log.LevelTrace)

		var m features.Feature
		if metaData, readErr := os.ReadFile(filepath.Join(featureDir, "devcontainer-feature.json")); readErr == nil {
			jsonc.Unmarshal(metaData, &m)
		}

		feat := features.Feature{
			ID:               cmp.Or(m.ID, ref.ID),
			Version:          m.Version,
			Name:             m.Name,
			Description:      m.Description,
			DocumentationURL: m.DocumentationURL,
			Value:            opts,
			UserOptions:      extractUserOptions(opts),
			Options:          m.Options,
			DependsOn:        m.DependsOn,
			DependsOnOrder:   m.DependsOnOrder,
			InstallsAfter:    m.InstallsAfter,
			LegacyIds:        m.LegacyIds,
			ContainerEnv:     m.ContainerEnv,
			Init:             m.Init,
			Privileged:       m.Privileged,
			CapAdd:           m.CapAdd,
			SecurityOpt:      m.SecurityOpt,
			Entrypoint:       m.Entrypoint,
			Mounts:           m.Mounts,
			Customizations:   m.Customizations,
			Included:         true,
			CachePath:        featureDir,
			Metadata:         m.Metadata,
			File:             m.Metadata,
			SourceKeys:       features.OCIFeatureKeys,
		}
		if feat.Version == "" {
			feat.Version = ref.Tag
		}

		userIDNoVer := featureIDWithoutVersion(id)

		set := &features.Set{
			SourceInfo: &features.OCISource{
				Type:     "oci",
				Registry: ref.Registry, Namespace: ref.Namespace,
				ID: ref.ID, Resource: ref.Resource, Tag: ref.Tag,
				ManifestDigest: manifest.ContentDigest, UserID: id,
				UserFeatureIdWithoutVersion: userIDNoVer,
				FeatureRef: &features.FeatureRef{
					Scheme:    userRef.Scheme(),
					ID:        userRef.ID,
					Owner:     userRef.Owner,
					Namespace: userRef.Namespace,
					Registry:  userRef.Registry,
					Resource:  userRef.Resource,
					Path:      userRef.Path,
					Version:   userRef.Version,
					Tag:       userRef.Tag,
					Digest:    userRef.Digest,
				},
				Manifest: features.ManifestJSON(manifest.ManifestBytes, manifest.Manifest),
			},
			Features:        []features.Feature{feat},
			ComputedDigest:  manifest.ContentDigest,
			InternalVersion: "2",
		}

		if manifest.Manifest.Annotations != nil {
			if metaJSON, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok {
				var am features.Feature
				if json.Unmarshal([]byte(metaJSON), &am) == nil {
					set.Features[0].LegacyIds = am.LegacyIds
					set.Features[0].DependsOn = am.DependsOn
					set.Features[0].DependsOnOrder = am.DependsOnOrder
					set.Features[0].InstallsAfter = am.InstallsAfter
					set.Features[0].Options = am.Options
					set.Features[0].Init = am.Init
					set.Features[0].Privileged = am.Privileged
					set.Features[0].CapAdd = am.CapAdd
					set.Features[0].SecurityOpt = am.SecurityOpt
					set.Features[0].Entrypoint = am.Entrypoint
					set.Features[0].Mounts = am.Mounts
					set.Features[0].Customizations = am.Customizations
					set.Features[0].Metadata = am.Metadata
					set.Features[0].ID = cmp.Or(am.ID, ref.ID)
				}
			}
		}
		return set, nil
	}
}

func featureIDWithoutVersion(id string) string {
	if loc := featureVersionDelimiter.FindStringIndex(id); loc != nil {
		return id[:loc[0]]
	}
	return id
}

var featureVersionDelimiter = regexp.MustCompile(`[:@][^/]*$`)

func tarballFeatureID(uri string) string {
	name := uri
	if u, err := url.Parse(uri); err == nil {
		name = u.Path
	}
	name = path.Base(name)
	if m := regexp.MustCompile(`devcontainer-feature-(.*)\.tgz`).FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

func realignFeatureDirs(dstFolder string, sets []*features.Set) error {
	for i, set := range sets {
		if len(set.Features) == 0 || set.Features[0].CachePath == "" {
			continue
		}
		consecutiveID := fmt.Sprintf("%s_%d", set.Features[0].ID, i)
		src := set.Features[0].CachePath
		dst := filepath.Join(dstFolder, consecutiveID)
		if src != dst {
			os.RemoveAll(dst)
			if err := os.Rename(src, dst); err != nil {
				if cpErr := copyDir(src, dst); cpErr != nil {
					return fmt.Errorf("stage feature %s: %w", consecutiveID, cpErr)
				}
				os.RemoveAll(src)
			}
			set.Features[0].CachePath = dst
		}
		set.Features[0].ConsecutiveId = consecutiveID
	}
	entries, _ := os.ReadDir(dstFolder)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), featureStagePrefix) {
			os.RemoveAll(filepath.Join(dstFolder, e.Name()))
		}
	}
	return nil
}

func stageFeatureBuildFiles(dstFolder string, sets []*features.Set, containerUser, remoteUser string) error {
	if err := os.WriteFile(filepath.Join(dstFolder, imagemeta.FeaturesBuiltinEnvFile), []byte(imagemeta.FeaturesBuiltinEnvContent(containerUser, remoteUser)), 0o644); err != nil {
		return fmt.Errorf("write Features builtin env: %w", err)
	}
	for _, set := range sets {
		for _, f := range set.Features {
			if f.CachePath == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(f.CachePath, "devcontainer-feature.json"))
			if err != nil {
				return fmt.Errorf("read Feature %s metadata: %w", f.ID, err)
			}
			std, err := jsonc.StripComments(data)
			if err != nil {
				return fmt.Errorf("parse Feature %s metadata: %w", f.ID, err)
			}
			info, err := imagemeta.ParseFeatureFileInfo(std)
			if err != nil {
				return fmt.Errorf("parse Feature %s metadata: %w", f.ID, err)
			}
			vars := imagemeta.FeatureEnvVariables(f, info)
			if err := os.WriteFile(filepath.Join(f.CachePath, imagemeta.FeatureEnvFile), []byte(strings.Join(vars, "\n")), 0o644); err != nil {
				return fmt.Errorf("write Feature %s env: %w", f.ID, err)
			}
			wrapper := imagemeta.FeatureInstallWrapperScript(f, set, info, vars)
			if err := os.WriteFile(filepath.Join(f.CachePath, imagemeta.FeatureInstallWrapperFile), []byte(wrapper), 0o644); err != nil {
				return fmt.Errorf("write Feature %s install wrapper: %w", f.ID, err)
			}
		}
	}
	return nil
}

func buildFeatureContentImage(ctx context.Context, dockerClient *docker.Client, folder string) error {
	dockerfile := filepath.Join(folder, "Dockerfile.buildContent")
	if err := os.WriteFile(dockerfile, []byte("\n\tFROM scratch\n\tCOPY . /tmp/build-features/\n\t"), 0o644); err != nil {
		return fmt.Errorf("write feature content Dockerfile: %w", err)
	}
	res, err := dockerClient.Build(ctx, docker.BuildOptions{
		Dockerfile:  dockerfile,
		ContextPath: folder,
		Tags:        []string{imagemeta.ComposeFeatureContentImage},
	})
	if err != nil {
		return fmt.Errorf("build feature content image: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("build feature content image failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}

func engineAtLeast23(ctx context.Context, engine *docker.EngineClient) bool {
	if engine == nil || engine.API == nil {
		return false
	}
	ver, err := engine.API.ServerVersion(ctx, mobyclient.ServerVersionOptions{})
	if err != nil {
		return false
	}
	majorText, _, _ := strings.Cut(ver.Version, ".")
	major, _ := strconv.Atoi(majorText)
	return major >= 23
}

// substituteFeatureHostVars resolves host-side variables (${localEnv:…},
// ${localWorkspaceFolder}, …) in each Feature's containerEnv, which the
// Features Dockerfile writes as ENV lines (upstream #308).
func substituteFeatureHostVars(sets []*features.Set, hs config.HostSubContext) error {
	resolver := config.NewVariableResolver()
	ctx := config.SubstitutionContext{HostSubContext: hs}
	for _, fs := range sets {
		for i := range fs.Features {
			f := &fs.Features[i]
			for k, v := range f.ContainerEnv {
				resolved, err := resolver.Resolve(ctx, config.PhaseHost, v)
				if err != nil {
					return fmt.Errorf("resolve Feature %s containerEnv %s: %w", f.ID, k, err)
				}
				if s, ok := resolved.(string); ok {
					f.ContainerEnv[k] = s
				}
			}
		}
	}
	return nil
}

func fetchBuildFeatures(ctx context.Context, logger log.Logger, featuresCfg map[string]any, fbOpts *FeatureBuildOptions) (*fetchFeatureResult, error) {
	if len(featuresCfg) == 0 {
		return nil, nil
	}
	var lockfile *features.Lockfile
	if fbOpts.ConfigPath != "" && !fbOpts.NoLockfile {
		var err error
		if lockfile, _, err = features.ReadLockfile(fbOpts.ConfigPath); err != nil {
			return nil, err
		}
	}
	result, err := fetchFeatureSetsWithOrder(ctx, logger, featuresCfg, localFeatureRoots{fbOpts.FeaturesBasePath, fbOpts.WorkspaceFolder}, lockfile, fbOpts.OverrideFeatureInstallOrder)
	if err != nil {
		return nil, err
	}
	if fbOpts.HostSub != nil {
		if err := substituteFeatureHostVars(result.FeatureSets, *fbOpts.HostSub); err != nil {
			return nil, err
		}
	}
	if !fbOpts.NoLockfile && fbOpts.ConfigPath != "" {
		lf := features.GenerateLockfile(&features.Config{FeatureSets: result.FeatureSets}, fbOpts.LockfileExcludeIDs)
		if err := features.WriteLockfile(fbOpts.ConfigPath, lf, features.WriteOptions{Frozen: fbOpts.FrozenLockfile}); err != nil {
			return nil, fmt.Errorf("lockfile: %w", err)
		}
	}
	return result, nil
}

func extendImageWithFeatures(
	ctx context.Context,
	logger log.Logger,
	dockerClient *docker.Client,
	engine *docker.EngineClient,
	baseImage string,
	featuresCfg map[string]any,
	useBuildx bool,
	imageNames []string,
	fbOpts *FeatureBuildOptions,
) ([]string, error) {
	result, err := fetchBuildFeatures(ctx, logger, featuresCfg, fbOpts)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return imageNames, nil
	}
	featureSets := result.FeatureSets
	tmpDir := result.TmpDir

	baseImageInfo, _ := engine.InspectImage(ctx, baseImage)

	metadata := []imagemeta.Entry{}
	var baseEntries []imagemeta.Entry
	if baseImageInfo.Config != nil {
		baseEntries = imagemeta.ReadMetadataFromLabels(baseImageInfo.Config.Labels, logger)
		metadata = append(metadata, baseEntries...)
	}
	for _, fs := range featureSets {
		metadata = append(metadata, featureMetadataEntry(fs, fbOpts.SkipPersistCustoms))
	}
	labelMetadata := append(slices.Clone(metadata), fbOpts.LabelMetadata...)
	metadata = append(metadata, fbOpts.AdditionalMetadata...)

	imageUser := "root"
	if baseImageInfo.Config != nil && baseImageInfo.Config.User != "" {
		imageUser = baseImageInfo.Config.User
	}
	containerUser, remoteUser := findContainerUsers(metadata, "", imageUser)

	var secretIDs []string
	if useBuildx {
		secretIDs = buildSecretIDs(fbOpts.Secrets)
	}
	caSecrets := caBundleBuildSecrets(fbOpts.CABundle, useBuildx, logger)
	if err := stageFeatureBuildFiles(tmpDir, featureSets, containerUser, remoteUser); err != nil {
		return nil, err
	}
	// TS dockerfilePrefixContent for an image (no Dockerfile of the user's):
	// Docker >= 23 needs no syntax directive; an older one needs
	// docker/dockerfile:1.4 for the build contexts.
	syntax := ""
	if useBuildx && !engineAtLeast23(ctx, engine) {
		syntax = "docker/dockerfile:1.4"
	}
	buildInfo := imagemeta.GenerateFeaturesBuildOptions(imagemeta.FeaturesBuildParams{
		BaseName:            baseImage,
		FeatureSets:         featureSets,
		Metadata:            labelMetadata,
		ContainerUser:       containerUser,
		RemoteUser:          remoteUser,
		ImageUser:           imageUser,
		UseBuildKitContexts: useBuildx,
		Syntax:              syntax,
		ConfigContainerEnv:  fbOpts.ContainerEnv,
		SecretIDs:           secretIDs,
		CABundle:            caSecrets != nil,
	})
	if !useBuildx {
		if err := buildFeatureContentImage(ctx, dockerClient, tmpDir); err != nil {
			return nil, err
		}
	}

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile.extended")
	content := buildInfo.DockerfilePrefixContent + buildInfo.DockerfileContent
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write extended Dockerfile: %w", err)
	}

	logger.Write("Generated feature Dockerfile:", log.LevelTrace)
	logger.Write(content, log.LevelTrace)

	outputName := baseImage + "-features"
	if len(imageNames) > 0 {
		outputName = imageNames[0]
	}

	tags := []string{outputName}
	if len(imageNames) > 1 {
		tags = imageNames
	}

	buildOpts := docker.BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: tmpDir,
		Tags:        tags,
		Target:      buildInfo.OverrideTarget,
		BuildArgs:   buildInfo.BuildArgs,
		UseBuildx:   useBuildx,
		NoCache:     fbOpts.NoCache,
		CacheFrom:   fbOpts.CacheFrom,
		CacheTo:     fbOpts.CacheTo,
		Labels:      fbOpts.Labels,
		Platform:    fbOpts.Platform,
		Push:        fbOpts.Push,
		Output:      fbOpts.Output,
		Secrets:     fbOpts.Secrets,
		FileSecrets: caSecrets,
	}

	for ctx, ctxPath := range buildInfo.BuildKitContexts {
		if ctxPath == "." {
			ctxPath = tmpDir
		}
		buildOpts.ExtraArgs = append(buildOpts.ExtraArgs, "--build-context", fmt.Sprintf("%s=%s", ctx, ctxPath))
	}

	// When the active buildx builder uses docker-container driver, it can't access
	// locally-built images (FROM $base fails with "pull access denied").
	// Find a builder with the "docker" driver that shares the host image store.
	if useBuildx {
		activeBuilder := dockerClient.DetectActiveBuilder(ctx)
		if activeBuilder.Driver == "docker-container" || activeBuilder.Driver == "remote" {
			dockerBuilder := dockerClient.FindDockerDriverBuilder(ctx)
			if dockerBuilder != "" {
				logger.Write(fmt.Sprintf("Active builder %q uses %s driver; switching to %q for feature build",
					activeBuilder.Name, activeBuilder.Driver, dockerBuilder), log.LevelTrace)
				buildOpts.ExtraArgs = append(buildOpts.ExtraArgs, "--builder", dockerBuilder)
			}
		}
	}

	authEnv, authCleanup := bridgeBuildAuth(logger, "", tags, buildOpts.CacheFrom, buildOpts.CacheTo)
	defer authCleanup()
	buildOpts.Env = authEnv

	buildResult, err := dockerClient.Build(ctx, buildOpts)
	if err != nil {
		return nil, fmt.Errorf("build extended image: %w", err)
	}
	if buildResult.ExitCode != 0 {
		return nil, fmt.Errorf("Command failed: feature build (exit %d): %s", buildResult.ExitCode, string(buildResult.Stderr))
	}

	logger.Write(string(buildResult.Stderr), log.LevelInfo)

	return tags, nil
}

func findContainerUsers(metadata []imagemeta.Entry, composeServiceUser, imageUser string) (containerUser, remoteUser string) {
	for _, e := range slices.Backward(metadata) {
		if containerUser == "" && e.ContainerUser != "" {
			containerUser = e.ContainerUser
		}
		if remoteUser == "" && e.RemoteUser != "" {
			remoteUser = e.RemoteUser
		}
	}
	if containerUser == "" {
		containerUser = composeServiceUser
	}
	if containerUser == "" {
		containerUser = imageUser
	}
	if remoteUser == "" {
		remoteUser = containerUser
	}
	return containerUser, remoteUser
}

func featureMetadataEntry(fs *features.Set, skipPersistCustoms bool) imagemeta.Entry {
	feat := fs.Features[0]
	id := feat.ID
	if fs.SourceInfo != nil && fs.SourceInfo.UserFeatureID() != "" {
		id = fs.SourceInfo.UserFeatureID()
	}
	// Note: a feature's containerEnv is NOT recorded in the metadata label — it
	// is baked into the image as ENV during the feature build. Recording it would
	// re-apply it at `docker run` via `-e` (with the literal ${PATH}), breaking
	// PATH. This matches the TS CLI's pickFeatureProperties, which omits
	// containerEnv/remoteEnv/remoteUser from feature metadata entries.
	entry := imagemeta.Entry{
		ID:          id,
		Init:        feat.Init,
		Privileged:  feat.Privileged,
		CapAdd:      feat.CapAdd,
		SecurityOpt: feat.SecurityOpt,
		Mounts:      feat.Mounts,
		Entrypoint:  feat.Entrypoint,
	}
	if !skipPersistCustoms {
		entry.Customizations = feat.Customizations
	}
	entry.OnCreateCommand = feat.OnCreateCommand
	entry.UpdateContentCommand = feat.UpdateContentCommand
	entry.PostCreateCommand = feat.PostCreateCommand
	entry.PostStartCommand = feat.PostStartCommand
	entry.PostAttachCommand = feat.PostAttachCommand
	return entry
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}

	var reader io.Reader
	gzr, gzErr := gzip.NewReader(f)
	if gzErr == nil {
		reader = gzr
		defer gzr.Close()
		defer f.Close()
	} else {
		f.Close()
		f2, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f2.Close()
		reader = f2
	}

	tr := tar.NewReader(reader)
	root, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("open extraction root: %w", err)
	}
	defer root.Close()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Tar paths always use forward slashes. Localize rejects absolute paths,
		// parent traversal, and names that cannot be represented safely on the
		// current platform; what it returns is always local, as filepath.IsLocal
		// reports. Root also prevents escapes through symlinks already present
		// below destDir.
		cleanName := path.Clean(header.Name)
		localName, err := filepath.Localize(cleanName)
		if err != nil {
			return fmt.Errorf("tar entry %q escapes the destination directory", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(localName, 0755); err != nil {
				return fmt.Errorf("create directory for tar entry %q: %w", header.Name, err)
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(localName), 0755); err != nil {
				return fmt.Errorf("create parent for tar entry %q: %w", header.Name, err)
			}
			out, err := root.Create(localName)
			if err != nil {
				return fmt.Errorf("create tar entry %q: %w", header.Name, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("extract tar entry %q: %w", header.Name, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close tar entry %q: %w", header.Name, err)
			}
			if header.Mode != 0 {
				if err := root.Chmod(localName, os.FileMode(header.Mode)); err != nil {
					return fmt.Errorf("set mode on tar entry %q: %w", header.Name, err)
				}
			}
		}
	}
	os.Remove(archivePath)
	return nil
}

func extractUserOptions(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func verifyTarballDigest(lockfile *features.Lockfile, uri, computedDigest string) error {
	if lockfile == nil {
		return nil
	}
	if want := lockfile.Features[uri].Integrity; want != "" && want != computedDigest {
		return &digestMismatchError{uri: uri}
	}
	return nil
}

type digestMismatchError struct{ uri string }

func (e *digestMismatchError) Error() string {
	return fmt.Sprintf("Digest did not match for %s.", e.uri)
}

func downloadFeatureTarball(ctx context.Context, logger log.Logger, tarballURL string, env map[string]string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range featureRequestHeaders(tarballURL, env, logger) {
		req.Header.Set(k, v)
	}
	if isPlainHTTPURL(tarballURL) && logger != nil {
		logger.Write("Sending as plain HTTP request", log.LevelWarning)
	}

	client := &http.Client{Transport: httpx.NewTransport(), Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("download %s: HTTP %d", tarballURL, resp.StatusCode)
	}
	data, err := readCapped(resp.Body, maxFeatureTarballBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", tarballURL, err)
	}
	sum := sha256.Sum256(data)
	return data, "sha256:" + hex.EncodeToString(sum[:]), nil
}

const maxFeatureTarballBytes = 512 << 20

func readCapped(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1)) // mutate-exempt: any limit past max reads more than max from a body over the cap, so max+2 fails exactly where max+1 does
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("larger than the %d MiB a Feature tarball may be", max>>20)
	}
	return data, nil
}

// Go's http.Client strips Authorization on a cross-host redirect, so a GitHub
// release asset's redirect to its presigned CDN URL does not carry the token.
func featureRequestHeaders(tarballURL string, env map[string]string, logger log.Logger) map[string]string {
	headers := map[string]string{"User-Agent": "devcontainer"}
	if isGitHubTarballURI(tarballURL) {
		if token := env["GITHUB_TOKEN"]; token != "" {
			if logger != nil {
				logger.Write("Using environment GITHUB_TOKEN.", log.LevelInfo)
			}
			headers["Authorization"] = "Bearer " + token
		} else if logger != nil {
			logger.Write("No environment GITHUB_TOKEN available.", log.LevelInfo)
		}
	}
	return headers
}

// isGitHubTarballURI reports whether GITHUB_TOKEN may be sent to u. TS checks
// only that the URI starts with "https://github.com" or "https://api.github.com",
// which also matches github.com.attacker.example and github.com@attacker.example;
// this CLI requires the host itself to be GitHub's (docs/DIVERGENCES.md). Case,
// a trailing dot and a port are allowed as in TS: they name the same host, and
// TLS verification still binds the token to GitHub's certificate.
func isGitHubTarballURI(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	return host == "github.com" || host == "api.github.com"
}

func isPlainHTTPURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Hostname() == "localhost"
}

func resolveFeatureID(logger log.Logger, id string) (string, features.SourceType, error) {
	resolvedID, warning := features.BackwardCompatibleID(id)
	if warning != "" {
		logger.Write(warning, log.LevelWarning)
	}
	srcType := features.ClassifyID(resolvedID)
	if srcType == features.SourceLegacyShorthand {
		return "", srcType, &coreerrors.ContainerError{Description: fmt.Sprintf(msgLegacyFeature, resolvedID)}
	}
	return resolvedID, srcType, nil
}

func featureSetIDs(sets []*features.Set) string {
	ids := make([]string, len(sets))
	for i, s := range sets {
		if len(s.Features) > 0 {
			ids[i] = s.Features[0].ID
		} else {
			ids[i] = "?"
		}
	}
	return strings.Join(ids, " → ")
}
