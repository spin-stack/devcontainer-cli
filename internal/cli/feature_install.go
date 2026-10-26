package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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

// FeatureBuildOptions holds extra build options to thread through feature install.
type FeatureBuildOptions struct {
	NoCache bool
	// HostSub, when set, resolves ${localEnv:…}/${localWorkspaceFolder}/… in each
	// Feature's containerEnv/mounts — same as the config's own substitution.
	HostSub   *config.HostSubContext
	CacheFrom []string
	CacheTo   string
	Labels    []string
	Platform  string
	Push      bool
	Output    string
	// Secrets are Docker build secrets ("KEY=VALUE") forwarded to the
	// feature-install image build as buildx `--secret id=KEY,env=KEY`, so a
	// Feature's install.sh can use `RUN --mount=type=secret` (#1078). Requires
	// buildx; ignored by the legacy builder.
	Secrets                []string
	ContainerEnv           map[string]string
	SkipFeatureAutoMapping bool
	SkipPersistCustoms     bool
	FeaturesBasePath       string
	// WorkspaceFolder is the workspace root local Features must lie under
	// (in its .devcontainer folder); "" stands for the working directory.
	WorkspaceFolder string
	// AdditionalMetadata follows the Features' entries in the metadata the
	// Features' users are found in (TS findContainerUsers), variables
	// substituted; LabelMetadata follows them in the image's
	// devcontainer.metadata label, as written (TS getDevcontainerMetadata's raw).
	AdditionalMetadata []imagemeta.Entry
	LabelMetadata      []imagemeta.Entry
	// devcontainer-lock.json is written with the resolved feature digests
	// unless NoLockfile is set, as TS generateFeaturesConfig writes it;
	// FrozenLockfile validates the resolved features against the committed
	// lockfile instead and aborts on drift. ConfigPath is the devcontainer.json
	// path used to locate the lockfile.
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

// fetchFeatureResult holds the fetched features and their features folder.
type fetchFeatureResult struct {
	FeatureSets []*features.Set
	// TmpDir is the features folder (TS dstFolder):
	// <cacheFolder>/container-features/<cliVersion>-<ms>, holding each Feature
	// at its consecutiveId. Like TS, the build paths keep it.
	TmpDir string
}

// fetchFeatureSets fetches features, without a lockfile or an install-order
// override, and returns them in install order.
func fetchFeatureSets(ctx context.Context, logger log.Logger, featuresCfg map[string]any, local localFeatureRoots, skipAutoMapping bool) (*fetchFeatureResult, error) {
	return fetchFeatureSetsWithOrder(ctx, logger, featuresCfg, local, skipAutoMapping, nil, nil)
}

// localFeatureRoots are what a local Feature reference (./x) resolves
// against: the folder of the configuration that lists it ("" when there is no
// configuration) and the workspace root (TS params.cwd: the workspace folder,
// or the working directory without one), whose .devcontainer folder the
// Feature must lie in.
type localFeatureRoots struct {
	configDir     string
	workspaceRoot string
}

// resolve returns a local Feature's folder. A reference TS
// processFeatureIdentifier refuses is logged as the error TS writes and
// reported as no Feature set (nil, nil), which the dependency graph fails as
// a Feature that could not be processed, as TS does.
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

// createFeaturesFolder creates the folder the Features are staged in, TS
// createFeaturesTempFolder: <cacheFolder>/container-features/<cliVersion>-<ms>.
func createFeaturesFolder() (string, error) {
	dir := filepath.Join(cacheFolder(), "container-features", fmt.Sprintf("%s-%d", cliVersion(), time.Now().UnixMilli()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create features folder: %w", err)
	}
	return dir, nil
}

// fetchFeatureSetsWithOrder resolves the feature dependency graph through the
// unified features.BuildDependencyGraph builder, applying
// overrideFeatureInstallOrder and returning the FeatureSets in install
// order. Each returned Set's content is staged in the features folder at its
// consecutiveId, `<feature id>_<installOrderIndex>`, as TS fetchFeatures does.
func fetchFeatureSetsWithOrder(ctx context.Context, logger log.Logger, featuresCfg map[string]any, local localFeatureRoots, skipAutoMapping bool, lockfile *features.Lockfile, overrideOrder []string) (*fetchFeatureResult, error) {
	if len(featuresCfg) == 0 {
		return nil, nil
	}

	// Deterministic user-feature order (Go maps iterate randomly).
	userFeatures := features.UserFeaturesToArray(featuresCfg)
	sort.Slice(userFeatures, func(i, j int) bool {
		return userFeatures[i].UserFeatureID < userFeatures[j].UserFeatureID
	})

	logger.Write(fmt.Sprintf("Installing %d feature(s)...", len(userFeatures)), log.LevelInfo)

	ociClient := newOCIClient(ctx, logger)

	dstFolder, err := createFeaturesFolder()
	if err != nil {
		return nil, err
	}

	// processFeature is the injected seam (TS processFeature): given a node's
	// UserFeatureID + Options, fetch/extract the feature and read its metadata
	// (annotation-first, blob-fallback for OCI). Each call stages content in its
	// own directory; the final install-order index dictates the real directory
	// name, applied later by realignFeatureDirs.
	var stageIdx int
	nextStageDir := func() string {
		dir := filepath.Join(dstFolder, fmt.Sprintf("%s%d", featureStagePrefix, stageIdx))
		stageIdx++
		return dir
	}

	processFeature := func(node *features.FNode) (*features.Set, error) {
		return processInstallFeature(logger, ociClient, node, local, skipAutoMapping, lockfile, nextStageDir)
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

// featureStagePrefix names the folders a Feature is fetched into before its
// install-order index, and so its consecutiveId, is known.
const featureStagePrefix = ".stage-"

// featureFromMetadata builds a features.Feature for a local or direct-tarball
// feature from its parsed devcontainer-feature.json (m), overriding the identity
// and staging fields the caller controls. OCI features build their Feature
// separately (they omit lifecycle commands here and merge annotation metadata
// afterwards), so this helper covers only the two identical local/tarball paths.
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
	}
}

// processInstallFeature is the install-path implementation of the processFeature
// seam. It resolves and extracts a single feature (local / direct-tarball / OCI)
// into a staging directory and returns a Set whose Features[0] carries the
// feature metadata, so the graph builder can expand its dependencies.
func processInstallFeature(
	logger log.Logger,
	ociClient oci.Registry,
	node *features.FNode,
	local localFeatureRoots,
	skipAutoMapping bool,
	lockfile *features.Lockfile,
	nextStageDir func() string,
) (*features.Set, error) {
	id := node.UserFeatureID
	opts := node.Options
	srcType := features.ClassifyID(id)

	if srcType == features.SourceLegacyShorthand {
		name := id
		// mutate-exempt: idx > 1 differs only for a colon at index 1, which leaves a one-character name, and no legacy Feature has one: both bounds reject the id with the same error
		if idx := strings.LastIndex(id, ":"); idx > 0 {
			name = id[:idx]
		}
		if !features.IsKnownLegacyFeature(name) {
			return nil, &coreerrors.ContainerError{Description: fmt.Sprintf(msgLegacyFeature, id)}
		}
		// gradle/maven/jupyterlab folded into java/python with an option
		// (installGradle, ...) — matching the TS deprecatedFeaturesIntoOptions.
		if m, ok := features.DeprecatedFeatureIntoOptions[name]; ok {
			logger.Write(fmt.Sprintf("(!) WARNING: Falling back to the deprecated '%s' Feature. It is now part of the '%s' Feature.", name, m.MapTo), log.LevelWarning)
			id = fmt.Sprintf("ghcr.io/devcontainers/features/%s:1", m.MapTo)
			opts = addFeatureOption(opts, m.Option)
			srcType = features.ClassifyID(id)
		}
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
		// devcontainer-feature.json is JSONC (comments/trailing commas allowed).
		if err := jsonc.Unmarshal(metaData, &m); err != nil {
			return nil, fmt.Errorf("parse local feature %q metadata: %w", id, err)
		}

		// TS: the Feature's id is the folder name and its name the user's
		// reference, until devcontainer-feature.json's id and name replace them.
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
			// Record the tarball digest so the lockfile pins it (TS parity: a
			// direct-tarball feature is locked by its computed sha256).
			ComputedDigest: computedDigest,
		}, nil

	default: // OCI (and legacy shorthand resolved to OCI)
		resolvedID, _ := features.ResolveID(id, skipAutoMapping)
		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  %w", id, err)
		}

		// Pin to the digest recorded in the lockfile (reproducible resolution),
		// matching the TS CLI which fetches the manifest by the lockfile integrity.
		if lockfile != nil {
			if integrity := lockfile.Features[id].Integrity; integrity != "" {
				if pinned, perr := oci.ParseRef(ref.Resource + "@" + integrity); perr == nil {
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
		os.MkdirAll(featureDir, 0o755)
		tgzPath := filepath.Join(featureDir, "feature.tgz")
		if err := os.WriteFile(tgzPath, blobData, 0o644); err != nil {
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
		}
		if feat.Version == "" {
			feat.Version = ref.Tag
		}

		userIDNoVer := featureIDWithoutVersion(id)
		owner, _, _ := strings.Cut(ref.Namespace, "/")

		set := &features.Set{
			SourceInfo: &features.OCISource{
				Type:     "oci",
				Registry: ref.Registry, Namespace: ref.Namespace,
				ID: ref.ID, Resource: ref.Resource, Tag: ref.Tag,
				ManifestDigest: manifest.ContentDigest, UserID: id,
				UserFeatureIdWithoutVersion: userIDNoVer,
				FeatureRef: map[string]string{
					"id": ref.ID, "namespace": ref.Namespace,
					"owner":    owner,
					"path":     ref.Namespace + "/" + ref.ID,
					"registry": ref.Registry,
					"scheme":   ref.Scheme(),
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
				}
			}
		}
		return set, nil
	}
}

// featureIDWithoutVersion is TS getFeatureIdWithoutVersion: the reference
// without its last `:tag` or `@digest` (a registry port is kept).
func featureIDWithoutVersion(id string) string {
	if loc := featureVersionDelimiter.FindStringIndex(id); loc != nil {
		return id[:loc[0]]
	}
	return id
}

var featureVersionDelimiter = regexp.MustCompile(`[:@][^/]*$`)

// tarballFeatureID is the id TS processFeatureIdentifier derives from a direct
// tarball's name, devcontainer-feature-<id>.tgz, used when the Feature's
// devcontainer-feature.json declares none.
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

// realignFeatureDirs moves each ordered Set's staging directory to
// <dstFolder>/<consecutiveId>, consecutiveId being TS's
// `${feature.id}_${installOrderIndex}`, so the generated Dockerfile's
// references resolve to the right content. Orphaned staging directories
// (duplicates and soft-dependency probes that are not installed) are removed so
// they don't bloat the build context.
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
	// Remove any leftover staging directories.
	entries, _ := os.ReadDir(dstFolder)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), featureStagePrefix) {
			os.RemoveAll(filepath.Join(dstFolder, e.Name()))
		}
	}
	return nil
}

// stageFeatureBuildFiles writes the files the features Dockerfile refers to, as
// TS getFeaturesBuildOptions does: devcontainer-features.builtin.env in the
// features folder, and devcontainer-features.env plus the
// devcontainer-features-install.sh wrapper in each Feature's folder. The option
// order, deprecation and currentId come from the Feature's staged
// devcontainer-feature.json, which TS spreads into the Feature.
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

// buildFeatureContentImage builds the temporary image holding the Features'
// content for a build without BuildKit build contexts (TS
// getFeaturesBuildOptions, non-BuildKit branch).
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

// engineAtLeast23 reports whether the daemon is Docker Engine >= 23 (TS
// skipDefaultSyntax: BuildKit's built-in frontend supports build contexts).
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

// extendImageWithFeatures fetches features from OCI, generates the extended
// Dockerfile, and builds the image with features installed.
// Returns the new image name.
// substituteFeatureHostVars resolves host-side variables (${localEnv:…},
// ${localWorkspaceFolder}, …) in each Feature's containerEnv values and mounts,
// so a Feature mount like "${localEnv:HOME}/.cfg" reaches Docker resolved —
// matching how the config's own containerEnv/mounts are substituted (upstream
// #308, which only substituted devcontainer.json's own values).
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
			if len(f.Mounts) > 0 {
				resolved, err := resolver.Resolve(ctx, config.PhaseHost, f.Mounts)
				if err != nil {
					return fmt.Errorf("resolve Feature %s mounts: %w", f.ID, err)
				}
				if m, ok := resolved.([]any); ok {
					f.Mounts = m
				}
			}
		}
	}
	return nil
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
	// Read the lockfile (if any) to pin feature resolution to recorded digests.
	// One that cannot be read fails the build, as TS generateFeaturesConfig's
	// readLockfile rethrows.
	var lockfile *features.Lockfile
	if fbOpts.ConfigPath != "" && !fbOpts.NoLockfile {
		var err error
		if lockfile, _, err = features.ReadLockfile(fbOpts.ConfigPath); err != nil {
			return nil, err
		}
	}
	result, err := fetchFeatureSetsWithOrder(ctx, logger, featuresCfg, localFeatureRoots{fbOpts.FeaturesBasePath, fbOpts.WorkspaceFolder}, fbOpts.SkipFeatureAutoMapping, lockfile, fbOpts.OverrideFeatureInstallOrder)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return imageNames, nil
	}
	featureSets := result.FeatureSets
	// The features folder is kept, as TS keeps it.
	tmpDir := result.TmpDir

	// Resolve ${localEnv:…} / ${localWorkspaceFolder} / … in each Feature's
	// containerEnv and mounts, like the config's own values (upstream #308).
	if fbOpts.HostSub != nil {
		if err := substituteFeatureHostVars(featureSets, *fbOpts.HostSub); err != nil {
			return nil, err
		}
	}

	// Write (or, frozen, validate) the features lockfile unless --no-lockfile
	// is given, as TS generateFeaturesConfig does (containerFeaturesConfiguration.ts
	// `if (!params.noLockfile) await writeLockfile(...)`).
	if !fbOpts.NoLockfile && fbOpts.ConfigPath != "" {
		lf := features.GenerateLockfile(&features.Config{FeatureSets: featureSets}, fbOpts.LockfileExcludeIDs)
		if err := features.WriteLockfile(fbOpts.ConfigPath, lf, features.WriteOptions{Frozen: fbOpts.FrozenLockfile}); err != nil {
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
		metadata = append(metadata, featureMetadataEntry(fs, fbOpts.SkipPersistCustoms))
	}
	labelMetadata := append(slices.Clone(metadata), fbOpts.LabelMetadata...)
	metadata = append(metadata, fbOpts.AdditionalMetadata...)

	// TS findContainerUsers over the image, Features and config metadata.
	imageUser := "root"
	if baseImageInfo.Config != nil && baseImageInfo.Config.User != "" {
		imageUser = baseImageInfo.Config.User
	}
	containerUser, remoteUser := findContainerUsers(metadata, "", imageUser)

	// Build secrets are mounted into each feature-install RUN, but only when the
	// build uses buildx (RUN --mount=type=secret + --secret both require BuildKit).
	var secretIDs []string
	if useBuildx {
		secretIDs = buildSecretIDs(fbOpts.Secrets)
	}
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
	})
	if !useBuildx {
		if err := buildFeatureContentImage(ctx, dockerClient, tmpDir); err != nil {
			return nil, err
		}
	}

	// Write the Dockerfile
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile.extended")
	content := buildInfo.DockerfilePrefixContent + buildInfo.DockerfileContent
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
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
		NoCache:     fbOpts.NoCache,
		CacheFrom:   fbOpts.CacheFrom,
		CacheTo:     fbOpts.CacheTo,
		Labels:      fbOpts.Labels,
		Platform:    fbOpts.Platform,
		Push:        fbOpts.Push,
		Output:      fbOpts.Output,
		Secrets:     fbOpts.Secrets,
	}

	// Add BuildKit contexts — "." is the features folder
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

	// Bridge the CLI credential chain for the push target / registry cache. The
	// base here is the locally-built intermediate image, so it needs no auth.
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

// findContainerUsers is TS findContainerUsers: the last containerUser of the
// metadata, else the compose service's user, else the image's user; the last
// remoteUser, else the containerUser.
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
	// TS getDevcontainerMetadata: the entry's id is the user's reference
	// (sourceInformation.userFeatureId) for every source type.
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
	// TS pickFeatureLifecycleHookProperties: every hook a Feature declares.
	entry.OnCreateCommand = feat.OnCreateCommand
	entry.UpdateContentCommand = feat.UpdateContentCommand
	entry.PostCreateCommand = feat.PostCreateCommand
	entry.PostStartCommand = feat.PostStartCommand
	entry.PostAttachCommand = feat.PostAttachCommand
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
			// A truncated copy must fail the install, not silently produce a
			// corrupt Feature that we report as success.
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

// extractUserOptions pulls out the user-provided options from the feature value.
func extractUserOptions(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// verifyTarballDigest rejects a direct tarball whose digest is not the one the
// lockfile records for its URI, with TS fetchContentsAtTarballUri's error, which
// reaches the user unwrapped.
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

// downloadFeatureTarball fetches a Feature published as a direct HTTP(S) tarball,
// matching the TS CLI's request/getRequestHeaders behavior instead of a bare GET:
//   - sends a "devcontainer" User-Agent;
//   - attaches Authorization: Bearer $GITHUB_TOKEN for github.com / api.github.com
//     URLs, so private release assets resolve (the naive GET 404'd on them);
//   - accepts any 2xx (TS treats 200-299 as success), not only 200;
//   - warns when downloading over plaintext HTTP;
//   - goes through the shared proxy- and custom-CA-aware transport.
//
// It returns the tarball bytes and their computed "sha256:<hex>" digest so the
// caller can pin/verify integrity (TS records and checks this digest).
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

// maxFeatureTarballBytes bounds a direct Feature tarball download.
const maxFeatureTarballBytes = 512 << 20

// readCapped reads r to the end, failing rather than truncating when it holds
// more than max bytes: a cut tarball would be extracted and its digest checked
// against the lockfile as if it were whole.
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

// featureRequestHeaders mirrors the TS getRequestHeaders for a direct-tarball
// source: always a "devcontainer" User-Agent, plus a bearer GITHUB_TOKEN when the
// tarball is hosted on GitHub. Redirects to a signed CDN URL drop the header —
// Go's http.Client strips Authorization on a cross-host redirect, which is the
// correct behavior for GitHub release assets (the redirect target is presigned).
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

// isGitHubTarballURI matches the TS isGitHubUri check (raw prefix, no parsing).
func isGitHubTarballURI(u string) bool {
	return strings.HasPrefix(u, "https://github.com") || strings.HasPrefix(u, "https://api.github.com")
}

// isPlainHTTPURL reports whether the URL would travel unencrypted (TS warns on
// this): a http:// scheme, or a localhost host regardless of scheme.
func isPlainHTTPURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Hostname() == "localhost"
}

// addFeatureOption returns the feature options with key=true added, preserving
// any existing options. Used to fold gradle/maven/jupyterlab into java/python.
func addFeatureOption(opts any, key string) map[string]any {
	m := map[string]any{}
	if existing, ok := opts.(map[string]any); ok {
		maps.Copy(m, existing)
	}
	m[key] = true
	return m
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
