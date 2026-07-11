package cli

import (
	"os"
	"strings"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// bridgeBuildAuth resolves credentials for every registry a build touches (base
// image, push tags, cache import/export) via the CLI's credential chain and, when
// any resolve, returns the subprocess env (DOCKER_CONFIG=<tmp>) plus a cleanup
// func. It returns (nil, no-op) when there is nothing to bridge, leaving the
// build's ambient auth untouched. Errors are swallowed (best-effort): a build
// that would have worked anonymously must not fail because auth bridging did.
func bridgeBuildAuth(logger log.Log, baseImage string, tags, cacheFrom []string, cacheTo string) (env []string, cleanup func()) {
	noop := func() {}
	regs := collectBuildRegistries(baseImage, tags, cacheFrom, cacheTo)
	if len(regs) == 0 {
		return nil, noop
	}
	dir, clean, ok, err := oci.ResolveBuildAuth(environMap(), regs, logger)
	if err != nil || !ok {
		return nil, noop
	}
	return []string{"DOCKER_CONFIG=" + dir}, clean
}

// collectBuildRegistries returns the deduped registry hosts referenced by a build.
func collectBuildRegistries(baseImage string, tags, cacheFrom []string, cacheTo string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(host string) {
		if host != "" && !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	add(registryHostFromImageRef(baseImage))
	for _, t := range tags {
		add(registryHostFromImageRef(t))
	}
	for _, cf := range cacheFrom {
		add(registryHostFromImageRef(registryCacheRef(cf)))
	}
	add(registryHostFromImageRef(registryCacheRef(cacheTo)))
	return out
}

// registryHostFromImageRef extracts the registry host from a docker image
// reference, following Docker's own rule: the substring before the first '/' is
// the registry only when it looks like a host (contains '.' or ':', or equals
// "localhost"). Otherwise the image lives on Docker Hub, for which ambient auth
// already applies — so we return "" and skip bridging.
func registryHostFromImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	i := strings.IndexByte(ref, '/')
	if i < 0 {
		return ""
	}
	first := ref[:i]
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return ""
}

// registryCacheRef pulls the image reference out of a buildx cache spec. A
// registry cache is written as "type=registry,ref=<image>[,...]"; anything else
// (type=local/inline/gha, or a bare image) is returned as-is.
func registryCacheRef(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || !strings.Contains(spec, "=") {
		return spec
	}
	for _, part := range strings.Split(spec, ",") {
		if strings.HasPrefix(part, "ref=") {
			return strings.TrimPrefix(part, "ref=")
		}
	}
	return ""
}

// environMap snapshots the process environment as a map for the credential chain.
func environMap() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}
