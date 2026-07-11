package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/httpx"
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

// enforceDisallowedFeatures blocks any configured feature that the containers.dev
// control manifest reports as problematic — mirroring the TS ensureNoDisallowedFeatures,
// which runs on every config read. Go had the machinery (GetControlManifest /
// EnsureNoDisallowedFeatures) but never invoked it, so the security blocklist was
// not enforced. Returns a ContainerError envelope for the first disallowed feature.
//
// cfg.Features is expected to already include any --additional-features (merged in
// by mergeAdditionalFeatures), so both are covered by checking it.
func enforceDisallowedFeatures(ctx context.Context, cfg *config.DevContainerConfig, logger log.Log) error {
	if cfg == nil || len(cfg.Features) == 0 {
		return nil
	}
	cf := cacheFolder()
	_ = os.MkdirAll(cf, 0o755)
	manifest := features.GetControlManifest(ctx, cf, httpx.New(cliVersion()), logger)
	if manifest == nil {
		return nil
	}
	err := features.EnsureNoDisallowedFeatures(manifest, cfg.Features, nil)
	if err == nil {
		return nil
	}
	var dfe *features.DisallowedFeatureError
	if errors.As(err, &dfe) {
		desc := fmt.Sprintf("Cannot use the '%s' Feature since it was reported to be problematic. Please remove this Feature from your configuration and rebuild any dev container using it before continuing.", dfe.FeatureID)
		if dfe.DocumentationURL != "" {
			desc += fmt.Sprintf(" See %s to learn more.", dfe.DocumentationURL)
		}
		// TS's error envelope for this path emits only outcome/message/description
		// (the disallowedFeatureId/learnMoreUrl live in the internal ContainerError
		// data, not the serialized output), so we mirror that and set Description only.
		return &coreerrors.ContainerError{Description: desc}
	}
	return err
}
