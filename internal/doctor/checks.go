package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The cache-export probe builds this trivial, network-free context. `FROM
// scratch` needs no image pull, and `--cache-to=type=local` exercises the cache
// export path that the plain `docker` buildx driver rejects.
const probeDockerfile = "FROM scratch\nLABEL devcontainer.doctor.probe=1\n"

const minFreeDiskBytes uint64 = 5 << 30

func checkDockerDaemon(ctx context.Context, env *Env) Result {
	r := Result{Name: "docker-daemon"}
	stdout, _, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		r.Status = StatusFail
		r.Summary = fmt.Sprintf("Docker CLI not found (%s)", env.dockerPath())
		r.Remediation = "Install Docker Engine: https://docs.docker.com/engine/install/"
		return r
	}
	if code != 0 {
		r.Status = StatusFail
		r.Summary = "Docker daemon is not reachable"
		r.Remediation = "Start Docker (e.g. `sudo systemctl start docker`) and ensure your user can access it."
		return r
	}
	r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
	r.Summary = fmt.Sprintf("Docker daemon reachable (server %s)", strings.TrimSpace(string(stdout)))
	return r
}

func checkBuildx(ctx context.Context, env *Env) Result {
	r := Result{Name: "buildx"}
	stdout, _, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "buildx", "version")
	if err != nil || code != 0 {
		r.Status = StatusWarn
		r.Summary = "docker buildx plugin is not available"
		r.Remediation = "Install the Docker buildx plugin: https://github.com/docker/buildx#installing"
		return r
	}
	r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
	r.Summary = firstLine(string(stdout))
	return r
}

func checkCacheExport(ctx context.Context, env *Env) Result {
	r := Result{Name: "build-cache-export", Fixable: true}

	dir, err := writeProbeContext(env.ProbeDir)
	if err != nil {
		r.Status = StatusWarn
		r.Summary = err.Error()
		return r
	}
	defer os.RemoveAll(dir)

	dest := filepath.Join(dir, "cache")
	_, stderr, code, err := env.runner().Run(ctx, nil, env.dockerPath(),
		"buildx", "build", "--cache-to", "type=local,dest="+dest, dir)
	if err == nil && code == 0 {
		r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
		r.Summary = "builder can export build cache"
		return r
	}

	r.Status = StatusWarn
	r.Summary = "active builder cannot export build cache (--cache-to/--output/--platform)"
	r.Remediation = "Run `devcontainer setup` to create a cache-capable builder, or enable the " +
		"containerd image store (add {\"features\":{\"containerd-snapshotter\":true}} to " +
		"/etc/docker/daemon.json and restart Docker)."
	if msg := firstLine(string(stderr)); msg != "" {
		r.Summary += ": " + msg
	}
	return r
}

func writeProbeContext(base string) (string, error) {
	dir, err := os.MkdirTemp(base, "devcontainer-doctor-*")
	if err != nil {
		return "", fmt.Errorf("could not create probe context: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(probeDockerfile), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("could not write probe context: %w", err)
	}
	return dir, nil
}

func checkComposeV2(ctx context.Context, env *Env) Result {
	r := Result{Name: "compose-v2"}
	stdout, _, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "compose", "version", "--short")
	if err != nil || code != 0 {
		r.Status = StatusWarn
		r.Summary = "docker compose (v2) plugin is not available"
		r.Remediation = "Install the Docker Compose v2 plugin (only needed for Compose-based dev containers)."
		return r
	}
	r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
	r.Summary = "docker compose v" + firstLine(string(stdout))
	return r
}

func checkDiskSpace(ctx context.Context, env *Env) Result {
	r := Result{Name: "disk-space"}
	path := dockerDataRoot(ctx, env)

	free := env.DiskFree
	if free == nil {
		free = statfsFree
	}
	avail, err := free(path)
	if err != nil {
		r.Status = StatusWarn
		r.Summary = fmt.Sprintf("could not determine free space on %s: %v", path, err)
		return r
	}
	if avail < minFreeDiskBytes {
		r.Status = StatusWarn
		r.Summary = fmt.Sprintf("low free disk space on %s: %s (recommended ≥ %s)",
			path, humanBytes(avail), humanBytes(minFreeDiskBytes))
		r.Remediation = "Free up space or run `docker system prune` to reclaim unused images/containers."
		return r
	}
	r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
	r.Summary = fmt.Sprintf("%s free on %s", humanBytes(avail), path)
	return r
}

func checkSELinux(_ context.Context, env *Env) Result {
	r := Result{Name: "selinux"}
	path := env.SELinuxEnforcePath
	if path == "" {
		path = "/sys/fs/selinux/enforce"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
		r.Summary = "SELinux not enabled"
		return r
	}
	if strings.TrimSpace(string(data)) != "1" {
		r.Status = StatusOK // mutate-exempt: StatusOK is Result's zero value, so dropping this write leaves the same Result
		r.Summary = "SELinux not enforcing"
		return r
	}
	r.Status = StatusWarn
	r.Summary = "SELinux is enforcing; workspace/bind mounts can fail with 'Permission denied' in the container"
	r.Remediation = `Relabel bind mounts for the container: add "runArgs": ["--security-opt", "label=disable"] ` +
		"to devcontainer.json, or append :z/:Z to bind-mount sources."
	return r
}

func dockerDataRoot(ctx context.Context, env *Env) string {
	stdout, _, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "info", "--format", "{{.DockerRootDir}}")
	if err == nil && code == 0 {
		if root := strings.TrimSpace(string(stdout)); root != "" {
			// The daemon's root may be unreadable from this host (remote/rootless);
			// use it only if it exists locally.
			if _, statErr := os.Stat(root); statErr == nil {
				return root
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func humanBytes(b uint64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.0f MiB", float64(b)/mib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
