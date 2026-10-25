package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/httpx"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
)

// cacheFolder mirrors the TS getCacheFolder: <tmp>/devcontainercli-<user> on
// Linux, <tmp>/devcontainercli elsewhere.
func cacheFolder() string {
	base := "devcontainercli"
	if runtime.GOOS == "linux" {
		if u, err := user.Current(); err == nil && u.Username != "" {
			base = "devcontainercli-" + u.Username
		}
	}
	return filepath.Join(os.TempDir(), base)
}

// enforceDisallowedFeatures blocks a configured feature that the containers.dev
// control manifest reports as problematic, as TS ensureNoDisallowedFeatures does
// on every config read. The first disallowed feature in the order the JSON lists
// them is reported. stopRunning, when given (up, with its id labels), stops the
// workspace's container if it is running, and the error says so; build passes
// nil, as TS build passes no id labels.
//
// cfg.Features is expected to already include any --additional-features (merged
// in by mergeAdditionalFeatures), which TS lists after the config's own.
func enforceDisallowedFeatures(ctx context.Context, cfg *config.DevContainer, additionalFeatures string, logger log.Logger, stopRunning func(context.Context) bool) *coreerrors.ContainerError {
	if cfg == nil || len(cfg.Features) == 0 {
		return nil
	}
	cf := cacheFolder()
	_ = os.MkdirAll(cf, 0o755)
	manifest := features.GetControlManifest(ctx, cf, controlManifestClient(), logger)
	if manifest == nil {
		return nil
	}
	dfe, ok := errors.AsType[*features.DisallowedFeatureError](features.EnsureNoDisallowedFeatures(manifest, featureIDsInOrder(cfg, additionalFeatures)))
	if !ok {
		return nil
	}
	stopped := stopRunning != nil && stopRunning(ctx)
	desc := fmt.Sprintf("Cannot use the '%s' Feature since it was reported to be problematic. Please remove this Feature from your configuration and rebuild any dev container using it before continuing.", dfe.FeatureID)
	if stopped {
		desc += " The existing dev container was stopped."
	}
	if dfe.DocumentationURL != "" {
		desc += fmt.Sprintf(" See %s to learn more.", dfe.DocumentationURL)
	}
	return &coreerrors.ContainerError{
		Description:         desc,
		DisallowedFeatureID: dfe.FeatureID,
		DidStopContainer:    &stopped,
		LearnMoreURL:        dfe.DocumentationURL,
	}
}

// featureIDsInOrder lists cfg.Features as TS Object.keys({...config.features,
// ...additionalFeatures}) does: the config file's features in document order,
// then the additional features in theirs. A key neither document lists (an
// --override-config one) follows, sorted.
func featureIDsInOrder(cfg *config.DevContainer, additionalFeatures string) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, id := range names {
			if _, ok := cfg.Features[id]; ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if data, err := os.ReadFile(cfg.ConfigFilePath); err == nil {
		add(jsonc.MemberNames(data, "features"))
	}
	if additionalFeatures != "" {
		add(jsonc.MemberNames([]byte(`{"features":`+additionalFeatures+`}`), "features"))
	}
	add(slices.Sorted(maps.Keys(cfg.Features)))
	return ids
}

// stopRunningContainer is TS ensureNoDisallowedFeatures' stop: the workspace's
// container, found by its id labels, is stopped with no grace period when it is
// running. It reports whether it stopped one.
func stopRunningContainer(engine *docker.EngineClient, idLabels []string) func(context.Context) bool {
	return func(ctx context.Context) bool {
		ids, err := engine.ListContainers(ctx, true, idLabels)
		if err != nil || len(ids) == 0 {
			return false
		}
		inspect, err := engine.InspectContainer(ctx, ids[0])
		if err != nil || inspect.State == nil || !inspect.State.Running {
			return false
		}
		return engine.StopContainerNow(ctx, ids[0]) == nil
	}
}

// logFeatureAdvisories warns about the fetched OCI features with an advisory
// for their version, as TS generateFeaturesConfig does after fetching them
// (logFeatureAdvisories). The control manifest is read only when there is an
// OCI feature to check.
func logFeatureAdvisories(ctx context.Context, logger log.Logger, sets []*features.Set) {
	if !slices.ContainsFunc(sets, func(s *features.Set) bool {
		_, ok := s.SourceInfo.(*features.OCISource)
		return ok
	}) {
		return
	}
	cf := cacheFolder()
	_ = os.MkdirAll(cf, 0o755)
	writeFeatureAdvisories(logger, features.GetControlManifest(ctx, cf, controlManifestClient(), logger), sets)
}

func writeFeatureAdvisories(logger log.Logger, manifest *features.ControlManifest, sets []*features.Set) {
	warn := func(s string) { logger.Write(s, log.LevelWarning) }
	if text := features.FormatFeatureAdvisories(features.FeatureAdvisories(manifest, sets, warn)); text != "" {
		warn(text)
	}
}

// controlManifestClient is the proxy- and CA-aware client for the control
// manifest, with the 30-second timeout the CLI always gave that request.
func controlManifestClient() *http.Client {
	return &http.Client{Transport: httpx.NewTransport(), Timeout: 30 * time.Second}
}
