package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// respond answers every docker invocation with the same outcome.
func respond(stdout, stderr string, code int, err error) *scriptRunner {
	return &scriptRunner{fn: func([]string) ([]byte, []byte, int, error) {
		return []byte(stdout), []byte(stderr), code, err
	}}
}

func TestChecksReportTheirExactResults(t *testing.T) {
	notFound := errors.New("exec: \"docker\": executable file not found in $PATH")
	tests := []struct {
		name  string
		check func(t *testing.T, env *Env) Result
		run   *scriptRunner
		want  Result
	}{
		{
			name: "daemon reachable", run: respond("27.0.3\n", "", 0, nil),
			check: func(t *testing.T, env *Env) Result { return checkDockerDaemon(t.Context(), env) },
			want:  Result{Name: "docker-daemon", Status: StatusOK, Summary: "Docker daemon reachable (server 27.0.3)"},
		},
		{
			name: "docker CLI missing", run: respond("", "", -1, notFound),
			check: func(t *testing.T, env *Env) Result { return checkDockerDaemon(t.Context(), env) },
			want: Result{Name: "docker-daemon", Status: StatusFail, Summary: "Docker CLI not found (docker)",
				Remediation: "Install Docker Engine: https://docs.docker.com/engine/install/"},
		},
		{
			name: "daemon unreachable", run: respond("", "Cannot connect to the Docker daemon", 1, nil),
			check: func(t *testing.T, env *Env) Result { return checkDockerDaemon(t.Context(), env) },
			want: Result{Name: "docker-daemon", Status: StatusFail, Summary: "Docker daemon is not reachable",
				Remediation: "Start Docker (e.g. `sudo systemctl start docker`) and ensure your user can access it."},
		},
		{
			name: "buildx missing", run: respond("", "unknown command", 1, nil),
			check: func(t *testing.T, env *Env) Result { return checkBuildx(t.Context(), env) },
			want: Result{Name: "buildx", Status: StatusWarn, Summary: "docker buildx plugin is not available",
				Remediation: "Install the Docker buildx plugin: https://github.com/docker/buildx#installing"},
		},
		{
			name: "buildx binary missing", run: respond("", "", 0, notFound),
			check: func(t *testing.T, env *Env) Result { return checkBuildx(t.Context(), env) },
			want: Result{Name: "buildx", Status: StatusWarn, Summary: "docker buildx plugin is not available",
				Remediation: "Install the Docker buildx plugin: https://github.com/docker/buildx#installing"},
		},
		{
			name: "buildx present", run: respond("\ngithub.com/docker/buildx v0.16.0\nmore\n", "", 0, nil),
			check: func(t *testing.T, env *Env) Result { return checkBuildx(t.Context(), env) },
			want:  Result{Name: "buildx", Status: StatusOK, Summary: "github.com/docker/buildx v0.16.0"},
		},
		{
			name: "compose missing", run: respond("", "unknown command", 1, nil),
			check: func(t *testing.T, env *Env) Result { return checkComposeV2(t.Context(), env) },
			want: Result{Name: "compose-v2", Status: StatusWarn, Summary: "docker compose (v2) plugin is not available",
				Remediation: "Install the Docker Compose v2 plugin (only needed for Compose-based dev containers)."},
		},
		{
			name: "compose binary missing", run: respond("2.29.0\n", "", 0, notFound),
			check: func(t *testing.T, env *Env) Result { return checkComposeV2(t.Context(), env) },
			want: Result{Name: "compose-v2", Status: StatusWarn, Summary: "docker compose (v2) plugin is not available",
				Remediation: "Install the Docker Compose v2 plugin (only needed for Compose-based dev containers)."},
		},
		{
			name: "compose present", run: respond("2.29.0\n", "", 0, nil),
			check: func(t *testing.T, env *Env) Result { return checkComposeV2(t.Context(), env) },
			want:  Result{Name: "compose-v2", Status: StatusOK, Summary: "docker compose v2.29.0"},
		},
		{
			name: "cache export works", run: respond("", "", 0, nil),
			check: func(t *testing.T, env *Env) Result { return checkCacheExport(t.Context(), env) },
			want:  Result{Name: "build-cache-export", Status: StatusOK, Fixable: true, Summary: "builder can export build cache"},
		},
		{
			name: "cache export fails without stderr", run: respond("", "", 0, notFound),
			check: func(t *testing.T, env *Env) Result { return checkCacheExport(t.Context(), env) },
			want: Result{Name: "build-cache-export", Status: StatusWarn, Fixable: true,
				Summary: "active builder cannot export build cache (--cache-to/--output/--platform)",
				Remediation: "Run `devcontainer setup` to create a cache-capable builder, or enable the " +
					"containerd image store (add {\"features\":{\"containerd-snapshotter\":true}} to " +
					"/etc/docker/daemon.json and restart Docker)."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &Env{Runner: tt.run, ProbeDir: t.TempDir()}
			if got := tt.check(t, env); got != tt.want {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestCacheExportBuildsTheProbeContextAndCleansUp(t *testing.T) {
	probeDir := t.TempDir()
	var dockerfile string
	sr := &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		ctxDir := args[len(args)-1]
		b, err := os.ReadFile(filepath.Join(ctxDir, "Dockerfile"))
		if err != nil {
			t.Errorf("probe context has no Dockerfile: %v", err)
		}
		dockerfile = string(b)
		return nil, nil, 0, nil
	}}
	got := checkCacheExport(t.Context(), &Env{Runner: sr, ProbeDir: probeDir})
	if got.Status != StatusOK {
		t.Fatalf("status = %s (%s), want ok", got.Status, got.Summary)
	}
	if dockerfile != probeDockerfile {
		t.Errorf("Dockerfile = %q, want %q", dockerfile, probeDockerfile)
	}
	if left, _ := os.ReadDir(probeDir); len(left) != 0 {
		t.Errorf("probe context left behind: %v", left)
	}
}

func TestCacheExportWarnsWhenTheProbeContextCannotBeCreated(t *testing.T) {
	sr := respond("", "", 0, nil)
	missing := filepath.Join(t.TempDir(), "missing")
	got := checkCacheExport(t.Context(), &Env{Runner: sr, ProbeDir: missing})
	if got.Name != "build-cache-export" || got.Status != StatusWarn || !got.Fixable || got.Remediation != "" {
		t.Fatalf("got %+v, want a fixable warn without remediation", got)
	}
	if !strings.HasPrefix(got.Summary, "could not create probe context: ") ||
		!strings.Contains(got.Summary, missing) || !strings.HasSuffix(got.Summary, "no such file or directory") {
		t.Errorf("summary = %q, want the probe-context creation error", got.Summary)
	}
	if len(sr.calls) != 0 {
		t.Errorf("docker must not run without a probe context; calls=%v", sr.calls)
	}
}

func TestDiskSpaceResults(t *testing.T) {
	root := t.TempDir()
	run := respond(root+"\n", "", 0, nil)

	t.Run("enough space", func(t *testing.T) {
		env := &Env{Runner: run, DiskFree: func(string) (uint64, error) { return 100 << 30, nil }}
		want := Result{Name: "disk-space", Status: StatusOK, Summary: "100.0 GiB free on " + root}
		if got := checkDiskSpace(t.Context(), env); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
	t.Run("free space unknown", func(t *testing.T) {
		env := &Env{Runner: run, DiskFree: func(string) (uint64, error) { return 0, errors.New("statfs boom") }}
		want := Result{Name: "disk-space", Status: StatusWarn, Summary: "could not determine free space on " + root + ": statfs boom"}
		if got := checkDiskSpace(t.Context(), env); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
	t.Run("low space", func(t *testing.T) {
		env := &Env{Runner: run, DiskFree: func(string) (uint64, error) { return 3 << 20, nil }}
		want := Result{Name: "disk-space", Status: StatusWarn,
			Summary:     "low free disk space on " + root + ": 3 MiB (recommended ≥ 5.0 GiB)",
			Remediation: "Free up space or run `docker system prune` to reclaim unused images/containers."}
		if got := checkDiskSpace(t.Context(), env); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

func TestDockerDataRootPrefersAReadableDaemonRoot(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	tests := []struct {
		name string
		run  *scriptRunner
		home string
		want string
	}{
		{"daemon root exists locally", respond("  "+root+"\n", "", 0, nil), home, root},
		{"info exits non-zero", respond(root, "", 1, nil), home, home},
		{"docker cannot run", respond(root, "", 0, errors.New("not found")), home, home},
		{"empty root", respond(" \n", "", 0, nil), home, home},
		{"root missing locally", respond(filepath.Join(root, "absent"), "", 0, nil), home, home},
		{"no home either", respond("", "", 1, nil), "", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", tt.home)
			if got := dockerDataRoot(t.Context(), &Env{Runner: tt.run}); got != tt.want {
				t.Fatalf("dockerDataRoot = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSELinuxSummaries(t *testing.T) {
	write := func(t *testing.T, content string) string {
		p := filepath.Join(t.TempDir(), "enforce")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name string
		path string
		want Result
	}{
		{"absent", filepath.Join(t.TempDir(), "enforce"), Result{Name: "selinux", Status: StatusOK, Summary: "SELinux not enabled"}},
		{"permissive", write(t, "0\n"), Result{Name: "selinux", Status: StatusOK, Summary: "SELinux not enforcing"}},
		{"enforcing", write(t, " 1\n"), Result{Name: "selinux", Status: StatusWarn,
			Summary: "SELinux is enforcing; workspace/bind mounts can fail with 'Permission denied' in the container",
			Remediation: `Relabel bind mounts for the container: add "runArgs": ["--security-opt", "label=disable"] ` +
				"to devcontainer.json, or append :z/:Z to bind-mount sources."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkSELinux(t.Context(), &Env{SELinuxEnforcePath: tt.path}); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHumanBytesBoundaries(t *testing.T) {
	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{1<<20 - 1, "1048575 B"},
		{1 << 20, "1 MiB"},
		{3 << 20, "3 MiB"},
		{1<<30 - 1<<20, "1023 MiB"},
		{1 << 30, "1.0 GiB"},
		{3 << 30, "3.0 GiB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStatfsFree(t *testing.T) {
	if free, err := statfsFree(t.TempDir()); err != nil || free == 0 {
		t.Errorf("statfsFree(tempdir) = %d, %v; want free bytes", free, err)
	}
	if free, err := statfsFree(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) || free != 0 {
		t.Errorf("statfsFree(missing) = %d, %v; want 0, ErrNotExist", free, err)
	}
}
