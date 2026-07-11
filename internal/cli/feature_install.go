package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/httpx"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
)

// FeatureBuildOptions holds extra build options to thread through feature install.
type FeatureBuildOptions struct {
	NoCache                bool
	CacheFrom              []string
	CacheTo                string
	Labels                 []string
	Platform               string
	Push                   bool
	Output                 string
	ContainerEnv           map[string]string
	SkipFeatureAutoMapping bool
	SkipPersistCustoms     bool
	FeaturesBasePath       string
	AdditionalMetadata     []imagemeta.Entry
	// Lockfile writes devcontainer-lock.json with the resolved feature digests;
	// FrozenLockfile validates the resolved features against the committed
	// lockfile and aborts on drift. ConfigPath is the devcontainer.json path
	// used to locate the lockfile.
	Lockfile       bool
	FrozenLockfile bool
	// NoLockfile disables lockfile reading (pinning-on-read) and writing, matching
	// TS --no-lockfile ("Disable lockfile generation and verification.").
	NoLockfile bool
	ConfigPath string
	// LockfileExcludeIDs holds userFeatureIds supplied only via
	// --additional-features; 0.88 (#11616) keeps these out of the lockfile.
	LockfileExcludeIDs map[string]bool
	// OverrideFeatureInstallOrder is cfg.overrideFeatureInstallOrder, threaded
	// into the dependency graph builder so install order honors it.
	OverrideFeatureInstallOrder []string
}

// extendImageWithFeatures fetches features from OCI, generates the extended
// Dockerfile, and builds the image with features installed.
// fetchFeatureResult holds fetched features and their temp directory.
type fetchFeatureResult struct {
	FeatureSets []*features.FeatureSet
	TmpDir      string // caller must os.RemoveAll when done
}

// fetchFeatureSets fetches features and returns them in install order. reg is the
// registry seam; pass nil for the default OCI client.
func fetchFeatureSets(logger log.Log, reg oci.Registry, featuresCfg map[string]interface{}, featuresBasePath string, skipAutoMapping bool, lockfile *features.Lockfile) (*fetchFeatureResult, error) {
	return fetchFeatureSetsWithOrder(logger, reg, featuresCfg, featuresBasePath, skipAutoMapping, lockfile, nil)
}

// fetchFeatureSetsWithOrder resolves the feature dependency graph through the
// unified features.BuildDependencyGraph builder, applying
// overrideFeatureInstallOrder and returning the FeatureSets in install
// order. Each returned FeatureSet's content is staged under the returned TmpDir
// at _dev_container_feature_<installOrderIndex>, matching the generated
// Dockerfile's COPY paths.
func fetchFeatureSetsWithOrder(logger log.Log, reg oci.Registry, featuresCfg map[string]interface{}, featuresBasePath string, skipAutoMapping bool, lockfile *features.Lockfile, overrideOrder []string) (*fetchFeatureResult, error) {
	if len(featuresCfg) == 0 {
		return nil, nil
	}

	// Deterministic user-feature order (Go maps iterate randomly).
	userFeatures := features.UserFeaturesToArray(featuresCfg)
	sort.Slice(userFeatures, func(i, j int) bool {
		return userFeatures[i].UserFeatureID < userFeatures[j].UserFeatureID
	})

	logger.Write(fmt.Sprintf("Installing %d feature(s)...", len(userFeatures)), log.LevelInfo)

	ociClient := reg
	if ociClient == nil {
		ociClient = oci.NewClient(logger, osEnvMap())
	}

	tmpDir, err := os.MkdirTemp("", "devcontainer-features-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// processFeature is the injected seam (TS processFeature): given a node's
	// UserFeatureID + Options, fetch/extract the feature and read its metadata
	// (annotation-first, blob-fallback for OCI). Each call stages content in its
	// own directory; the final install-order index dictates the real directory
	// name, applied later by realignFeatureDirs.
	stageIdx := 0
	nextStageDir := func() string {
		dir := filepath.Join(tmpDir, fmt.Sprintf("_dev_container_feature_stage_%d", stageIdx))
		stageIdx++
		return dir
	}

	processFeature := func(node *features.FNode) (*features.FeatureSet, error) {
		return processInstallFeature(logger, ociClient, node, featuresBasePath, skipAutoMapping, lockfile, nextStageDir)
	}

	graph, err := features.BuildDependencyGraph(logger, processFeature, userFeatures, overrideOrder, lockfile)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	if len(graph) == 0 {
		os.RemoveAll(tmpDir)
		return nil, nil
	}

	orderedSets, err := features.ComputeInstallationOrder(logger, graph, nil)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("compute Feature install order: %w", err)
	}

	if err := realignFeatureDirs(tmpDir, orderedSets); err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	logger.Write(fmt.Sprintf("Install order: %s", featureSetIDs(orderedSets)), log.LevelTrace)
	return &fetchFeatureResult{FeatureSets: orderedSets, TmpDir: tmpDir}, nil
}

// processInstallFeature is the install-path implementation of the processFeature
// seam. It resolves and extracts a single feature (local / direct-tarball / OCI)
// into a staging directory and returns a FeatureSet whose Features[0] carries the
// feature metadata, so the graph builder can expand its dependencies.
func processInstallFeature(
	logger log.Log,
	ociClient oci.Registry,
	node *features.FNode,
	featuresBasePath string,
	skipAutoMapping bool,
	lockfile *features.Lockfile,
	nextStageDir func() string,
) (*features.FeatureSet, error) {
	id := node.UserFeatureID
	opts := node.Options
	srcType := features.ClassifyFeatureID(id)

	if srcType == features.SourceLegacyShorthand {
		name := id
		if idx := strings.LastIndex(id, ":"); idx > 0 {
			name = id[:idx]
		}
		if !features.IsKnownLegacyFeature(name) {
			return nil, fmt.Errorf(msgLegacyFeature, id)
		}
		// gradle/maven/jupyterlab folded into java/python with an option
		// (installGradle, ...) — matching the TS deprecatedFeaturesIntoOptions.
		if m, ok := features.DeprecatedFeatureIntoOptions[name]; ok {
			logger.Write(fmt.Sprintf("(!) WARNING: Falling back to the deprecated '%s' Feature. It is now part of the '%s' Feature.", name, m.MapTo), log.LevelWarning)
			id = fmt.Sprintf("ghcr.io/devcontainers/features/%s:1", m.MapTo)
			opts = addFeatureOption(opts, m.Option)
			srcType = features.ClassifyFeatureID(id)
		}
	}

	switch srcType {
	case features.SourceLocalPath:
		resolvedPath := id
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(featuresBasePath, resolvedPath)
		}
		resolvedPath = filepath.Clean(resolvedPath)

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
		// devcontainer-feature.json is JSONC (comments/trailing commas allowed).
		if err := jsonc.Unmarshal(metaData, &m); err != nil {
			return nil, fmt.Errorf("parse local feature %q metadata: %w", id, err)
		}

		feat := features.Feature{
			ID:                   id,
			Version:              m.Version,
			Name:                 m.Name,
			Description:          m.Description,
			DocumentationURL:     m.DocumentationURL,
			Value:                opts,
			UserOptions:          extractUserOptions(opts),
			Options:              m.Options,
			DependsOn:            m.DependsOn,
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
			CachePath:            featureDir,
		}
		return &features.FeatureSet{
			SourceInfo:      &features.LocalSource{LocalPath: id, ResolvedPath: resolvedPath, UserID: id},
			Features:        []features.Feature{feat},
			InternalVersion: "2",
		}, nil

	case features.SourceDirectTarball:
		featureDir := nextStageDir()
		pfs.MkdirAll(featureDir)

		logger.Write(fmt.Sprintf("Fetching feature tarball %s...", id), log.LevelInfo)
		blobData, dlErr := downloadFeatureTarball(id)
		if dlErr != nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %v", id, dlErr)
		}
		tgzPath := filepath.Join(featureDir, "feature.tgz")
		if err := pfs.WriteFile(tgzPath, blobData); err != nil {
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
			featID = filepath.Base(featureDir)
		}

		feat := features.Feature{
			ID:                   featID,
			Version:              m.Version,
			Name:                 m.Name,
			Description:          m.Description,
			DocumentationURL:     m.DocumentationURL,
			Value:                opts,
			UserOptions:          extractUserOptions(opts),
			Options:              m.Options,
			DependsOn:            m.DependsOn,
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
			CachePath:            featureDir,
		}
		return &features.FeatureSet{
			SourceInfo:      &features.TarballSource{TarballURI: id, UserID: id},
			Features:        []features.Feature{feat},
			InternalVersion: "2",
		}, nil

	default: // OCI (and legacy shorthand resolved to OCI)
		resolvedID, _ := features.ResolveFeatureID(id, skipAutoMapping)
		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %v", id, err)
		}

		// Pin to the digest recorded in the lockfile (reproducible resolution),
		// matching the TS CLI which fetches the manifest by the lockfile integrity.
		if lockfile != nil {
			if entry, ok := lockfile.Features[id]; ok && entry.Integrity != "" {
				if pinned, perr := oci.ParseRef(ref.Resource + "@" + entry.Integrity); perr == nil {
					ref = pinned
				}
			}
		}

		logger.Write(fmt.Sprintf("Fetching feature %s...", ref.Resource), log.LevelInfo)

		manifest, err := ociClient.FetchManifest(ref, "")
		if err != nil {
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
		pfs.MkdirAll(featureDir)
		tgzPath := filepath.Join(featureDir, "feature.tgz")
		if err := pfs.WriteFile(tgzPath, blobData); err != nil {
			return nil, fmt.Errorf("write feature tarball: %w", err)
		}
		if err := extractTarGz(tgzPath, featureDir); err != nil {
			return nil, fmt.Errorf("extract feature %q: %w", id, err)
		}
		logger.Write(fmt.Sprintf("Extracted feature %s (%d bytes)", ref.ID, len(blobData)), log.LevelTrace)

		// Blob-fallback metadata: the extracted devcontainer-feature.json.
		var m features.Feature
		if metaData, readErr := os.ReadFile(filepath.Join(featureDir, "devcontainer-feature.json")); readErr == nil {
			json.Unmarshal(metaData, &m)
		}

		feat := features.Feature{
			ID:               ref.ID,
			Version:          m.Version,
			Name:             m.Name,
			Description:      m.Description,
			DocumentationURL: m.DocumentationURL,
			Value:            opts,
			UserOptions:      extractUserOptions(opts),
			Options:          m.Options,
			DependsOn:        m.DependsOn,
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
		}
		if feat.Version == "" {
			feat.Version = ref.Tag
		}

		userIDNoVer := id
		if idx := strings.LastIndex(id, ":"); idx > 0 {
			userIDNoVer = id[:idx]
		}

		set := &features.FeatureSet{
			SourceInfo: &features.OCISource{
				Type:     "oci",
				Registry: ref.Registry, Namespace: ref.Namespace,
				ID: ref.ID, Resource: ref.Resource, Tag: ref.Tag,
				ManifestDigest: manifest.ContentDigest, UserID: id,
				UserFeatureIdWithoutVersion: userIDNoVer,
				FeatureRef: map[string]string{
					"id": ref.ID, "namespace": ref.Namespace,
					"owner":    strings.SplitN(ref.Namespace, "/", 2)[0],
					"path":     ref.Namespace + "/" + ref.ID,
					"registry": ref.Registry,
					"resource": ref.Resource,
					"tag":      ref.Tag, "version": ref.Tag,
				},
				Manifest: manifest.Manifest,
			},
			Features:        []features.Feature{feat},
			ComputedDigest:  manifest.ContentDigest,
			InternalVersion: "2",
		}

		// Annotation-first metadata: overrides the blob-derived dependency fields.
		if manifest.Manifest.Annotations != nil {
			if metaJSON, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok {
				var am features.Feature
				if json.Unmarshal([]byte(metaJSON), &am) == nil {
					set.Features[0].LegacyIds = am.LegacyIds
					set.Features[0].DependsOn = am.DependsOn
					set.Features[0].InstallsAfter = am.InstallsAfter
					set.Features[0].Options = am.Options
					set.Features[0].Init = am.Init
					set.Features[0].Privileged = am.Privileged
					set.Features[0].CapAdd = am.CapAdd
					set.Features[0].SecurityOpt = am.SecurityOpt
					set.Features[0].Entrypoint = am.Entrypoint
					set.Features[0].Mounts = am.Mounts
					set.Features[0].Customizations = am.Customizations
				}
			}
		}
		return set, nil
	}
}

// realignFeatureDirs renames each ordered FeatureSet's staging directory to
// _dev_container_feature_<installOrderIndex> under tmpDir, so the generated
// Dockerfile's `COPY _dev_container_feature_<i>` resolves to the right content.
// Orphaned staging directories (duplicates and soft-dependency probes that are
// not installed) are removed so they don't bloat the build context.
func realignFeatureDirs(tmpDir string, sets []*features.FeatureSet) error {
	keep := make(map[string]bool, len(sets))
	for i, set := range sets {
		if len(set.Features) == 0 || set.Features[0].CachePath == "" {
			continue
		}
		src := set.Features[0].CachePath
		dst := filepath.Join(tmpDir, fmt.Sprintf("_dev_container_feature_%d", i))
		if src != dst {
			os.RemoveAll(dst)
			if err := os.Rename(src, dst); err != nil {
				if cpErr := copyDir(src, dst); cpErr != nil {
					return fmt.Errorf("stage feature %d: %w", i, cpErr)
				}
				os.RemoveAll(src)
			}
			set.Features[0].CachePath = dst
		}
		// consecutiveId is the TS `${feature.id}_${idx}` identity (NOT the staging
		// directory name, which stays _dev_container_feature_<i> for the COPY).
		set.Features[0].ConsecutiveId = fmt.Sprintf("%s_%d", set.Features[0].ID, i)
		keep[dst] = true
	}
	// Remove any leftover staging directories.
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "_dev_container_feature_stage_") {
			os.RemoveAll(filepath.Join(tmpDir, e.Name()))
		}
	}
	return nil
}

// extendImageWithFeatures fetches features from OCI, generates the extended
// Dockerfile, and builds the image with features installed.
// Returns the new image name.
func extendImageWithFeatures(
	ctx context.Context,
	logger log.Log,
	dockerClient *docker.Client,
	engine *docker.EngineClient,
	baseImage string,
	featuresCfg map[string]interface{},
	useBuildx bool,
	imageNames []string,
	fbOpts *FeatureBuildOptions,
) ([]string, error) {
	skipAutoMap := fbOpts != nil && fbOpts.SkipFeatureAutoMapping
	featuresBasePath := ""
	if fbOpts != nil {
		featuresBasePath = fbOpts.FeaturesBasePath
	}
	// Read the lockfile (if any) to pin feature resolution to recorded digests.
	var lockfile *features.Lockfile
	if fbOpts != nil && fbOpts.ConfigPath != "" && !fbOpts.NoLockfile {
		lockfile, _, _ = features.ReadLockfile(fbOpts.ConfigPath)
	}
	var overrideOrder []string
	if fbOpts != nil {
		overrideOrder = fbOpts.OverrideFeatureInstallOrder
	}
	result, err := fetchFeatureSetsWithOrder(logger, nil, featuresCfg, featuresBasePath, skipAutoMap, lockfile, overrideOrder)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return imageNames, nil
	}
	featureSets := result.FeatureSets
	tmpDir := result.TmpDir
	defer os.RemoveAll(tmpDir)

	// Write/validate the features lockfile when requested (opt-in via the
	// --experimental-lockfile / --experimental-frozen-lockfile flags). Writing
	// records the resolved digests for reproducibility; frozen aborts on drift.
	if fbOpts != nil && (fbOpts.Lockfile || fbOpts.FrozenLockfile) && fbOpts.ConfigPath != "" {
		lf := features.GenerateLockfile(&features.FeaturesConfig{FeatureSets: featureSets}, fbOpts.LockfileExcludeIDs)
		if err := features.WriteLockfile(fbOpts.ConfigPath, lf, fbOpts.FrozenLockfile, fbOpts.Lockfile); err != nil {
			return nil, fmt.Errorf("lockfile: %w", err)
		}
	}

	// Inspect base image once — used for metadata labels and user resolution.
	baseImageInfo, _ := engine.InspectImage(ctx, baseImage)

	metadata := []imagemeta.Entry{}
	var baseEntries []imagemeta.Entry
	if baseImageInfo.Config != nil {
		baseEntries = imagemeta.ReadMetadataFromLabels(baseImageInfo.Config.Labels, logger)
		metadata = append(metadata, baseEntries...)
	}
	for _, fs := range featureSets {
		skipPersistCustoms := fbOpts != nil && fbOpts.SkipPersistCustoms
		metadata = append(metadata, featureMetadataEntry(fs, skipPersistCustoms))
	}
	if fbOpts != nil && len(fbOpts.AdditionalMetadata) > 0 {
		metadata = append(metadata, fbOpts.AdditionalMetadata...)
	}

	// Resolve containerUser/remoteUser matching TS findContainerUsers():
	// metadata containerUser → image Config.User → "root"
	// metadata remoteUser → containerUser
	containerUser := "root"
	remoteUser := "root"
	if baseImageInfo.Config != nil {
		if baseImageInfo.Config.User != "" {
			containerUser = baseImageInfo.Config.User
		}
		if len(baseEntries) > 0 {
			baseMerged := imagemeta.MergeConfiguration(baseEntries)
			if baseMerged.ContainerUser != "" {
				containerUser = baseMerged.ContainerUser
			}
			if baseMerged.RemoteUser != "" {
				remoteUser = baseMerged.RemoteUser
			}
		}
	}
	if remoteUser == "root" {
		remoteUser = containerUser
	}

	// Collect config-level containerEnv if provided
	var configContainerEnv map[string]string
	if fbOpts != nil {
		configContainerEnv = fbOpts.ContainerEnv
	}

	buildInfo := imagemeta.GenerateExtendImageBuild(
		baseImage, featureSets, metadata, containerUser, remoteUser, useBuildx, configContainerEnv,
	)

	// Write the Dockerfile
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile.extended")
	content := buildInfo.DockerfilePrefixContent + buildInfo.DockerfileContent
	if err := pfs.WriteFile(dockerfilePath, []byte(content)); err != nil {
		return nil, fmt.Errorf("write extended Dockerfile: %w", err)
	}

	logger.Write("Generated feature Dockerfile:", log.LevelTrace)
	logger.Write(content, log.LevelTrace)

	// Determine output image name
	outputName := baseImage + "-features"
	if len(imageNames) > 0 {
		outputName = imageNames[0]
	}

	// Build — thread through extra options
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
	}
	if fbOpts != nil {
		buildOpts.NoCache = fbOpts.NoCache
		buildOpts.CacheFrom = fbOpts.CacheFrom
		buildOpts.CacheTo = fbOpts.CacheTo
		buildOpts.Labels = fbOpts.Labels
		buildOpts.Platform = fbOpts.Platform
		buildOpts.Push = fbOpts.Push
		buildOpts.Output = fbOpts.Output
	}

	// Add BuildKit contexts — resolve relative paths to tmpDir
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
		activeBuilder := dockerClient.DetectActiveBuilder()
		if activeBuilder.Driver == "docker-container" || activeBuilder.Driver == "remote" {
			dockerBuilder := dockerClient.FindDockerDriverBuilder()
			if dockerBuilder != "" {
				logger.Write(fmt.Sprintf("Active builder %q uses %s driver; switching to %q for feature build",
					activeBuilder.Name, activeBuilder.Driver, dockerBuilder), log.LevelTrace)
				buildOpts.ExtraArgs = append(buildOpts.ExtraArgs, "--builder", dockerBuilder)
			}
		}
	}

	buildResult, err := dockerClient.Build(buildOpts)
	if err != nil {
		return nil, fmt.Errorf("build extended image: %w", err)
	}
	if buildResult.ExitCode != 0 {
		return nil, fmt.Errorf("Command failed: feature build (exit %d): %s", buildResult.ExitCode, string(buildResult.Stderr))
	}

	logger.Write(string(buildResult.Stderr), log.LevelInfo)

	if len(imageNames) > 0 {
		return imageNames, nil
	}
	return []string{outputName}, nil
}

func featureMetadataEntry(fs *features.FeatureSet, skipPersistCustoms bool) imagemeta.Entry {
	feat := fs.Features[0]
	id := feat.ID
	if src, ok := fs.SourceInfo.(*features.OCISource); ok && src.UserID != "" {
		id = src.UserID
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
	if feat.OnCreateCommand != nil {
		entry.OnCreateCommand = feat.OnCreateCommand
	}
	if feat.PostCreateCommand != nil {
		entry.PostCreateCommand = feat.PostCreateCommand
	}
	if feat.PostStartCommand != nil {
		entry.PostStartCommand = feat.PostStartCommand
	}
	if feat.PostAttachCommand != nil {
		entry.PostAttachCommand = feat.PostAttachCommand
	}
	return entry
}

// extractTarGz extracts a .tar or .tgz to a directory.
// Tries gzip first; falls back to plain tar.
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}

	// Try gzip first
	var reader io.Reader
	gzr, gzErr := gzip.NewReader(f)
	if gzErr == nil {
		reader = gzr
		defer gzr.Close()
		defer f.Close()
	} else {
		// Not gzip — reopen as plain tar
		f.Close()
		f2, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f2.Close()
		reader = f2
	}

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		cleanName := filepath.Clean(header.Name)
		target := filepath.Join(destDir, cleanName)

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			io.Copy(out, tr)
			out.Close()
			if header.Mode != 0 {
				os.Chmod(target, os.FileMode(header.Mode))
			}
		}
	}
	os.Remove(archivePath)
	return nil
}

// extractUserOptions pulls out the user-provided options from the feature value.
func extractUserOptions(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// downloadFeatureTarball fetches a Feature published as a direct HTTP(S) tarball.
func downloadFeatureTarball(url string) ([]byte, error) {
	client := &http.Client{Transport: httpx.NewTransport(), Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20)) // 512 MiB cap
}

// addFeatureOption returns the feature options with key=true added, preserving
// any existing options. Used to fold gradle/maven/jupyterlab into java/python.
func addFeatureOption(opts interface{}, key string) map[string]interface{} {
	m := map[string]interface{}{}
	if existing, ok := opts.(map[string]interface{}); ok {
		for k, v := range existing {
			m[k] = v
		}
	}
	m[key] = true
	return m
}

func featureSetIDs(sets []*features.FeatureSet) string {
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
