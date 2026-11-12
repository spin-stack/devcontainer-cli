package cli

import (
	"strings"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

func bridgeBuildAuth(logger log.Logger, baseImage string, tags, cacheFrom []string, cacheTo string) (env []string, cleanup func()) {
	regs := collectBuildRegistries(baseImage, tags, cacheFrom, cacheTo)
	dir, clean, ok, err := oci.ResolveBuildAuth(osEnvMap(), regs, logger)
	if err != nil || !ok {
		return nil, func() {}
	}
	return []string{"DOCKER_CONFIG=" + dir}, clean
}

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
	first, _, found := strings.Cut(ref, "/")
	if !found {
		return ""
	}
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return ""
}

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
