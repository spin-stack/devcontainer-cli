package cli

import (
	"crypto/x509"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	dockermount "github.com/moby/moby/api/types/mount"
)

const caBundleFlagUsage = "PEM bundle of CAs to trust: a build secret `" + imagemeta.CABundleSecretID + "` for Feature installs and Dockerfile RUNs, and a read-only mount at " + imagemeta.CABundlePath + " in the container (requires buildx for builds)."

const caBundleComposeBuildWarning = "--ca-bundle does not reach a Docker Compose configuration's build: `docker compose build` takes no build secrets."

func resolveCABundle(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("--ca-bundle: %w", err)
	}
	pem, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("--ca-bundle: %w", err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("--ca-bundle: %s holds no PEM certificate", abs)
	}
	return abs, nil
}

func caBundleBuildSecrets(path string, useBuildx bool, logger log.Logger) map[string]string {
	if path == "" {
		return nil
	}
	if !useBuildx {
		logger.Write("--ca-bundle requires BuildKit (buildx); the bundle is not given to the legacy builder", log.LevelWarning)
		return nil
	}
	return map[string]string{imagemeta.CABundleSecretID: path}
}

func caBundleMount(path string) dockermount.Mount {
	return dockermount.Mount{Type: dockermount.TypeBind, Source: path, Target: imagemeta.CABundlePath, ReadOnly: true}
}

func withCABundleEnv(env map[string]string, path string) map[string]string {
	if path == "" {
		return env
	}
	out := maps.Clone(env)
	if out == nil {
		out = map[string]string{}
	}
	for _, kv := range imagemeta.CABundleEnv() {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}
