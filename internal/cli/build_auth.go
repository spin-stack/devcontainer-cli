package cli

import (
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
func bridgeBuildAuth(logger log.Logger, baseImage string, tags, cacheFrom []string, cacheTo string) (env []string, cleanup func()) {
	// With no registry to bridge, ResolveBuildAuth resolves nothing and is not ok.
	regs := collectBuildRegistries(baseImage, tags, cacheFrom, cacheTo)
	dir, clean, ok, err := oci.ResolveBuildAuth(osEnvMap(), regs, logger)
	if err != nil || !ok {
		return nil, func() {}
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
	for part := range strings.SplitSeq(spec, ",") {
		if after, ok := strings.CutPrefix(part, "ref="); ok {
			return after
		}
	}
	return ""
}
