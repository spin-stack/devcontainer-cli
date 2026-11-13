package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func isImageBuild(c dockerCall) bool {
	return len(c) > 0 && c[0] == "build" || len(c) > 1 && c[0] == "buildx" && c[1] == "build"
}

func imageBuilds(calls []dockerCall) []dockerCall {
	var builds []dockerCall
	for _, c := range calls {
		if isImageBuild(c) {
			builds = append(builds, c)
		}
	}
	return builds
}

func argAfter(c dockerCall, flag string) string {
	if i := slices.Index(c, flag); i >= 0 && i+1 < len(c) {
		return c[i+1]
	}
	return ""
}

func readOnlyWorkspace(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only folders")
	}
	ws := writeFixture(t, files)
	var dirs []string
	listing := workspaceListing(t, ws)
	if err := filepath.WalkDir(ws, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			os.Chmod(d, 0o755)
		}
	})
	return ws, listing
}

func workspaceListing(t *testing.T, ws string) []string {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(ws, func(p string, _ os.DirEntry, err error) error {
		files = append(files, p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBuildDockerfileBuildsOnce(t *testing.T) {
	for name, features := range map[string]string{
		"without Features": "",
		"with a Feature":   `,"features":{"./hello":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			ws, listing := readOnlyWorkspace(t, map[string]string{
				".devcontainer/devcontainer.json":               `{"build":{"dockerfile":"Dockerfile"}` + features + `}`,
				".devcontainer/Dockerfile":                      "FROM alpine:3\nRUN true\n",
				".devcontainer/hello/devcontainer-feature.json": `{"id":"hello","version":"1.0.0"}`,
				".devcontainer/hello/install.sh":                "#!/bin/sh\n",
			})
			loadResult, err := config.LoadDevContainerConfig(ws, "", "")
			if err != nil {
				t.Fatal(err)
			}
			r := &fakeDockerRunner{}
			run := &buildRunner{
				log:    log.Null,
				docker: fakeDockerClient(r),
				engine: &docker.EngineClient{API: &fakeDockerAPI{version: "25.0.0", imageConfig: &dockerspec.DockerOCIImageConfig{}}},
				opts:   &buildOpts{workspaceFolder: ws, imageNames: []string{"img"}, noLockfile: true},
			}
			names, err := run.buildDockerfile(t.Context(), loadResult.Config, loadResult, true)
			if err != nil {
				t.Fatalf("build of a read-only workspace: %v", err)
			}
			if !slices.Equal(names, []string{"img"}) {
				t.Errorf("image names = %v, want [img]", names)
			}
			builds := imageBuilds(r.calls)
			if len(builds) != 1 {
				t.Fatalf("docker builds = %v, want one", builds)
			}
			b := builds[0]
			if f := argAfter(b, "-f"); filepath.Base(f) != featuresDockerfileName || !strings.HasPrefix(f, filepath.Join(os.Getenv("TMPDIR"), "devcontainercli")) {
				t.Errorf("-f %s, want the cache folder's %s", f, featuresDockerfileName)
			}
			if argAfter(b, "--target") != "dev_containers_target_stage" || !slices.Equal(tagsOf(b), []string{"img"}) {
				t.Errorf("build = %v, want --target dev_containers_target_stage -t img", b)
			}
			if got := workspaceListing(t, ws); !slices.Equal(got, listing) {
				t.Errorf("workspace holds %v after the build, want %v", got, listing)
			}
		})
	}
}

var errStopAfterBuild = errors.New("stop after the build")

func TestUpDockerfileBuildsOnce(t *testing.T) {
	for name, features := range map[string]string{
		"without Features": "",
		"with a Feature":   `,"features":{"./hello":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			ws, listing := readOnlyWorkspace(t, map[string]string{
				".devcontainer/devcontainer.json":               `{"build":{"dockerfile":"Dockerfile"}` + features + `}`,
				".devcontainer/Dockerfile":                      "FROM alpine:3\nRUN true\n",
				".devcontainer/hello/devcontainer-feature.json": `{"id":"hello","version":"1.0.0"}`,
				".devcontainer/hello/install.sh":                "#!/bin/sh\n",
			})
			loadResult, err := config.LoadDevContainerConfig(ws, "", "")
			if err != nil {
				t.Fatal(err)
			}
			r := &fakeDockerRunner{}
			run := &upRunner{
				log:        log.Null,
				docker:     fakeDockerClient(r),
				engine:     &docker.EngineClient{API: &fakeDockerAPI{version: "25.0.0", imageConfig: &dockerspec.DockerOCIImageConfig{}}},
				opts:       &upOpts{workspaceFolder: ws, noLockfile: true, updateRemoteUserUIDDefault: "never"},
				substitute: func(*[]imagemeta.Entry) error { return errStopAfterBuild },
			}
			if _, err := run.fromDockerfile(t.Context(), loadResult.Config, loadResult, ws, nil, true); !errors.Is(err, errStopAfterBuild) {
				t.Fatalf("up of a read-only workspace: %v, want it to reach the container", err)
			}
			builds := imageBuilds(r.calls)
			if len(builds) != 1 {
				t.Fatalf("docker builds = %v, want one", builds)
			}
			b := builds[0]
			if got, want := tagsOf(b), []string{folderImageName(ws)}; !slices.Equal(got, want) {
				t.Errorf("tags = %v, want %v", got, want)
			}
			if f := argAfter(b, "-f"); filepath.Base(f) != featuresDockerfileName || argAfter(b, "--target") != "dev_containers_target_stage" {
				t.Errorf("build = %v, want -f the cache folder's %s --target dev_containers_target_stage", b, featuresDockerfileName)
			}
			if got := workspaceListing(t, ws); !slices.Equal(got, listing) {
				t.Errorf("workspace holds %v after the build, want %v", got, listing)
			}
		})
	}
}

type dockerfileBuildRun struct {
	files       map[string]string
	config      config.DevContainer
	useBuildx   bool
	names       []string
	fb          FeatureBuildOptions
	imageConfig *dockerspec.DockerOCIImageConfig
	reply       func([]string) (string, int, error)
}

type dockerfileBuildResult struct {
	ws         string
	err        error
	calls      []dockerCall
	build      dockerCall
	dockerfile string
	path       string
}

func (d dockerfileBuildRun) run(t *testing.T) dockerfileBuildResult {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	ws := writeFixture(t, d.files)
	cfg := d.config
	cfg.ConfigFilePath = filepath.Join(ws, ".devcontainer", "devcontainer.json")
	fb := d.fb
	fb.FeaturesBasePath, fb.WorkspaceFolder, fb.NoLockfile = filepath.Dir(cfg.ConfigFilePath), ws, true
	names := d.names
	if names == nil {
		names = []string{"img"}
	}
	imageConfig := d.imageConfig
	if imageConfig == nil {
		imageConfig = &dockerspec.DockerOCIImageConfig{}
	}
	r := &fakeDockerRunner{reply: d.reply}
	err := buildDockerfileImage(t.Context(), log.Null, fakeDockerClient(r), &docker.EngineClient{API: &fakeDockerAPI{version: "25.0.0", imageConfig: imageConfig}},
		&cfg, d.useBuildx, names, fb.Platform, &fb)
	res := dockerfileBuildResult{ws: ws, err: err, calls: r.calls}
	if builds := imageBuilds(r.calls); len(builds) > 0 {
		res.build = builds[len(builds)-1]
		res.path = argAfter(res.build, "-f")
		data, rerr := os.ReadFile(res.path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		res.dockerfile = string(data)
	}
	return res
}

func TestBuildDockerfileWritesTheReferenceDockerfile(t *testing.T) {
	t.Run("without Features", func(t *testing.T) {
		res := dockerfileBuildRun{
			files: map[string]string{
				".devcontainer/Dockerfile": "# syntax=docker/dockerfile:1\nFROM alpine:3\nRUN echo hi > /hi\n",
			},
			config:    config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile", Args: map[string]string{"A": "1"}}},
			useBuildx: true,
			names:     []string{"tsparity-ws1"},
			fb:        FeatureBuildOptions{LabelMetadata: []imagemeta.Entry{{RemoteUser: "root"}}},
		}.run(t)
		if res.err != nil {
			t.Fatal(res.err)
		}
		const want = "# syntax=docker/dockerfile:1\n" +
			"    ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n" +
			"FROM alpine:3 AS dev_container_auto_added_stage_label\n" +
			"RUN echo hi > /hi\n" +
			"\n" +
			"\n" +
			"FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\n" +
			"LABEL devcontainer.metadata=\"[ \\\n" +
			"{\\\"remoteUser\\\":\\\"root\\\"} \\\n" +
			"]\"\n"
		if res.dockerfile != want {
			t.Errorf("Dockerfile =\n%q\nwant\n%q", res.dockerfile, want)
		}
		wantArgs := dockerCall{"buildx", "build", "--load", "--build-arg", "BUILDKIT_INLINE_CACHE=1",
			"-f", res.path, "-t", "tsparity-ws1", "--target", "dev_containers_target_stage",
			"--build-arg", "A=1", "--build-arg", "_DEV_CONTAINERS_BASE_IMAGE=dev_container_auto_added_stage_label",
			filepath.Join(res.ws, ".devcontainer")}
		if !reflect.DeepEqual(res.build, wantArgs) {
			t.Errorf("build =\n%v\nwant\n%v", res.build, wantArgs)
		}
	})
	t.Run("with a Feature", func(t *testing.T) {
		res := dockerfileBuildRun{
			files: map[string]string{
				".devcontainer/Dockerfile":                      "FROM alpine:3\nRUN echo hi > /hi\n",
				".devcontainer/hello/devcontainer-feature.json": `{"id":"hello","version":"1.0.0","name":"Hello"}`,
				".devcontainer/hello/install.sh":                "#!/bin/sh\necho installed > /hello\n",
			},
			config:    config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}, Features: map[string]any{"./hello": map[string]any{}}},
			useBuildx: true,
			names:     []string{"tsparity-ws2"},
		}.run(t)
		if res.err != nil {
			t.Fatal(res.err)
		}
		const want = "\n" +
			"ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n" +
			"FROM alpine:3 AS dev_container_auto_added_stage_label\n" +
			"RUN echo hi > /hi\n" +
			"\n" +
			"\n" +
			"\n" +
			"\n" +
			"\n" +
			"ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n" +
			"\n" +
			"FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_feature_content_normalize\n" +
			"USER root\n" +
			"COPY --from=dev_containers_feature_content_source devcontainer-features.builtin.env /tmp/build-features/\n" +
			"RUN chmod -R 0755 /tmp/build-features/\n" +
			"\n" +
			"FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\n" +
			"\n" +
			"USER root\n" +
			"\n" +
			"RUN mkdir -p /tmp/dev-container-features\n" +
			"COPY --from=dev_containers_feature_content_normalize /tmp/build-features/ /tmp/dev-container-features\n" +
			"\n" +
			"RUN \\\n" +
			"echo \"_CONTAINER_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)\" >> /tmp/dev-container-features/devcontainer-features.builtin.env && \\\n" +
			"echo \"_REMOTE_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)\" >> /tmp/dev-container-features/devcontainer-features.builtin.env\n" +
			"\n" +
			"\n" +
			"RUN --mount=type=bind,from=dev_containers_feature_content_source,source=hello_0,target=/tmp/build-features-src/hello_0 \\\n" +
			"    cp -ar /tmp/build-features-src/hello_0 /tmp/dev-container-features \\\n" +
			" && chmod -R 0755 /tmp/dev-container-features/hello_0 \\\n" +
			" && cd /tmp/dev-container-features/hello_0 \\\n" +
			" && chmod +x ./devcontainer-features-install.sh \\\n" +
			" && ./devcontainer-features-install.sh \\\n" +
			" && rm -rf /tmp/dev-container-features/hello_0\n" +
			"\n" +
			"\n" +
			"\n" +
			"\n" +
			"\n" +
			"ARG _DEV_CONTAINERS_IMAGE_USER=root\n" +
			"USER $_DEV_CONTAINERS_IMAGE_USER\n" +
			"\n" +
			"LABEL devcontainer.metadata=\"[ \\\n" +
			"{\\\"id\\\":\\\"./hello\\\"} \\\n" +
			"]\"\n" +
			"\n" +
			"\n"
		if res.dockerfile != want {
			t.Errorf("Dockerfile =\n%q\nwant\n%q", res.dockerfile, want)
		}
		folder := filepath.Dir(res.path)
		wantArgs := dockerCall{"buildx", "build", "--load", "--build-arg", "BUILDKIT_INLINE_CACHE=1",
			"-f", res.path, "-t", "tsparity-ws2", "--target", "dev_containers_target_stage",
			"--build-context", "dev_containers_feature_content_source=" + folder,
			"--build-arg", "_DEV_CONTAINERS_BASE_IMAGE=dev_container_auto_added_stage_label",
			"--build-arg", "_DEV_CONTAINERS_IMAGE_USER=root",
			"--build-arg", "_DEV_CONTAINERS_FEATURE_CONTENT_SOURCE=dev_container_feature_content_temp",
			filepath.Join(res.ws, ".devcontainer")}
		if !reflect.DeepEqual(res.build, wantArgs) {
			t.Errorf("build =\n%v\nwant\n%v", res.build, wantArgs)
		}
	})
}

func TestBuildDockerfileStage(t *testing.T) {
	t.Run("a configured target is the base stage, the Dockerfile kept", func(t *testing.T) {
		res := dockerfileBuildRun{
			files:  map[string]string{".devcontainer/Dockerfile": "FROM alpine AS base\nFROM base\n"},
			config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile", Target: "base"}},
		}.run(t)
		if res.err != nil {
			t.Fatal(res.err)
		}
		if !strings.Contains(res.dockerfile, "\nFROM alpine AS base\nFROM base\n\n") || !slices.Contains(res.build, "_DEV_CONTAINERS_BASE_IMAGE=base") {
			t.Errorf("Dockerfile %q, build %v; want the user's Dockerfile as is on base", res.dockerfile, res.build)
		}
		if argAfter(res.build, "--target") != "dev_containers_target_stage" {
			t.Errorf("build %v, want --target dev_containers_target_stage", res.build)
		}
	})
	for name, cfg := range map[string]config.DevContainer{
		"build":     {Build: &config.Build{Dockerfile: "Dockerfile"}},
		"top level": {DockerFile: "Dockerfile"},
	} {
		t.Run("the unnamed final stage is named, "+name, func(t *testing.T) {
			res := dockerfileBuildRun{
				files:  map[string]string{".devcontainer/Dockerfile": "FROM alpine\n"},
				config: cfg,
			}.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			if !strings.Contains(res.dockerfile, "FROM alpine AS dev_container_auto_added_stage_label\n") {
				t.Errorf("Dockerfile = %q, want the final stage named", res.dockerfile)
			}
			if data, err := os.ReadFile(filepath.Join(res.ws, ".devcontainer", "Dockerfile")); err != nil || string(data) != "FROM alpine\n" {
				t.Errorf("user's Dockerfile = %q, %v; want it untouched", data, err)
			}
		})
	}
	t.Run("a Dockerfile without FROM", func(t *testing.T) {
		res := dockerfileBuildRun{
			files:  map[string]string{".devcontainer/Dockerfile": "RUN true\n"},
			config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}},
		}.run(t)
		if res.err == nil || !strings.Contains(res.err.Error(), "no FROM instructions") || res.build != nil {
			t.Errorf("err = %v, build = %v; want the parse error and no build", res.err, res.build)
		}
	})
}

func TestBuildDockerfileKeepsTheSiblingDockerignore(t *testing.T) {
	res := dockerfileBuildRun{
		files: map[string]string{
			".devcontainer/Dockerfile":              "FROM alpine\n",
			".devcontainer/Dockerfile.dockerignore": "secret\n",
		},
		config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}},
	}.run(t)
	if res.err != nil {
		t.Fatal(res.err)
	}
	if data, err := os.ReadFile(res.path + ".dockerignore"); err != nil || string(data) != "secret\n" {
		t.Errorf("%s.dockerignore = %q, %v; want the user's", res.path, data, err)
	}
}

func TestBuildDockerfileMetadataLabel(t *testing.T) {
	base := &dockerspec.DockerOCIImageConfig{}
	base.Labels = map[string]string{imagemeta.MetadataLabel: `[{"id":"base"}]`}
	for name, tc := range map[string]struct {
		features map[string]any
		want     []string
	}{
		"without Features": {nil, []string{`"id":"base"`, `"id":"stage"`, `"remoteUser":"dev"`}},
		"with a Feature":   {map[string]any{"./hello": map[string]any{}}, []string{`"id":"base"`, `"id":"stage"`, `"id":"./hello"`, `"remoteUser":"dev"`}},
	} {
		t.Run(name, func(t *testing.T) {
			res := dockerfileBuildRun{
				files: map[string]string{
					".devcontainer/Dockerfile":                      "FROM alpine\nLABEL devcontainer.metadata='[{\"id\":\"stage\"}]'\n",
					".devcontainer/hello/devcontainer-feature.json": `{"id":"hello","version":"1.0.0"}`,
					".devcontainer/hello/install.sh":                "#!/bin/sh\n",
				},
				config:      config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}, Features: tc.features},
				imageConfig: base,
				fb:          FeatureBuildOptions{LabelMetadata: []imagemeta.Entry{{RemoteUser: "dev"}}},
			}.run(t)
			if res.err != nil {
				t.Fatal(res.err)
			}
			label := res.dockerfile[strings.LastIndex(res.dockerfile, "LABEL "+imagemeta.MetadataLabel):]
			label = strings.ReplaceAll(label, `\"`, `"`)
			at := 0
			for _, w := range tc.want {
				i := strings.Index(label[at:], w)
				if i < 0 {
					t.Fatalf("label %s: %s missing or out of order, want %v", label, w, tc.want)
				}
				at += i + len(w)
			}
		})
	}
}

func TestBuildDockerfileBuildFlags(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	res := dockerfileBuildRun{
		files: map[string]string{".devcontainer/Dockerfile": "FROM alpine AS dev\n"},
		config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile", Options: []string{"--network=host"},
			CacheFrom: config.StringOrStrings{"cfg-cache"}}},
		useBuildx: true,
		fb: FeatureBuildOptions{Push: true, Platform: "linux/arm64", CacheFrom: []string{"flag-cache"}, CacheTo: "type=local,dest=c",
			Labels: []string{"a=b"}, Secrets: []string{"TOKEN=v"}, CABundle: bundle, NoCache: true},
	}.run(t)
	if res.err != nil {
		t.Fatal(res.err)
	}
	b := res.build
	for _, want := range [][]string{
		{"--platform", "linux/arm64"}, {"--push"}, {"--cache-to", "type=local,dest=c"},
		{"--secret", "id=TOKEN,env=TOKEN"}, {"--secret", "id=" + imagemeta.CABundleSecretID + ",src=" + bundle},
		{"--no-cache", "--pull"}, {"--cache-from", "flag-cache", "--cache-from", "cfg-cache"},
		{"--label", "a=b", "--network=host", "--build-arg", "_DEV_CONTAINERS_BASE_IMAGE=dev"},
	} {
		if !containsRun(b, want) {
			t.Errorf("build %v lacks %v", b, want)
		}
	}
	if slices.Contains(b, "--load") || slices.Contains(b, "--output") {
		t.Errorf("build %v: --push takes the place of --load", b)
	}
	output := dockerfileBuildRun{
		files:     map[string]string{".devcontainer/Dockerfile": "FROM alpine AS dev\n"},
		config:    config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}, Features: map[string]any{"./hello": map[string]any{}}},
		useBuildx: true,
		fb:        FeatureBuildOptions{Output: "type=oci,dest=out.tar"},
	}
	output.files[".devcontainer/hello/devcontainer-feature.json"] = `{"id":"hello","version":"1.0.0"}`
	output.files[".devcontainer/hello/install.sh"] = "#!/bin/sh\n"
	if res := output.run(t); res.err != nil || !containsRun(res.build, []string{"--output", "type=oci,dest=out.tar"}) || len(imageBuilds(res.calls)) != 1 {
		t.Errorf("with a Feature: err %v, builds %v; want one build with --output", res.err, imageBuilds(res.calls))
	}
}

func containsRun(c dockerCall, run []string) bool {
	for i := 0; i+len(run) <= len(c); i++ {
		if slices.Equal(c[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

func TestBuildDockerfileWithoutBuildKit(t *testing.T) {
	res := dockerfileBuildRun{
		files: map[string]string{
			".devcontainer/Dockerfile":                      "FROM alpine\n",
			".devcontainer/hello/devcontainer-feature.json": `{"id":"hello","version":"1.0.0"}`,
			".devcontainer/hello/install.sh":                "#!/bin/sh\n",
		},
		config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}, Features: map[string]any{"./hello": map[string]any{}}},
		fb:     FeatureBuildOptions{Secrets: []string{"TOKEN=v"}},
	}.run(t)
	if res.err != nil {
		t.Fatal(res.err)
	}
	builds := imageBuilds(res.calls)
	if len(builds) != 2 || !slices.Contains(builds[0], imagemeta.ComposeFeatureContentImage) {
		t.Fatalf("builds = %v, want the content image then the image", builds)
	}
	for _, flag := range []string{"--build-context", "--secret", "--load", "BUILDKIT_INLINE_CACHE=1"} {
		if slices.Contains(res.build, flag) {
			t.Errorf("build %v has %s", res.build, flag)
		}
	}
	if !strings.Contains(res.dockerfile, "FROM "+imagemeta.ComposeFeatureContentImage+" as dev_containers_feature_content_source") {
		t.Errorf("Dockerfile = %q, want the content image stage", res.dockerfile)
	}
}

func TestBuildDockerfileFailures(t *testing.T) {
	files := map[string]string{".devcontainer/Dockerfile": "FROM alpine\n"}
	for name, tc := range map[string]struct {
		run  dockerfileBuildRun
		want string
	}{
		"no Dockerfile":      {dockerfileBuildRun{files: files, config: config.DevContainer{}}, "no Dockerfile specified"},
		"missing Dockerfile": {dockerfileBuildRun{files: files, config: config.DevContainer{Build: &config.Build{Dockerfile: "Missing"}}}, "read Dockerfile"},
		"docker fails":       {dockerfileBuildRun{files: files, config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}}, reply: func([]string) (string, int, error) { return "", 0, errors.New("no docker") }}, "exec docker: no docker"},
		"build fails":        {dockerfileBuildRun{files: files, config: config.DevContainer{Build: &config.Build{Dockerfile: "Dockerfile"}}, reply: func([]string) (string, int, error) { return "", 3, nil }}, "Command failed: docker build (exit 3)"},
	} {
		t.Run(name, func(t *testing.T) {
			if res := tc.run.run(t); res.err == nil || !strings.Contains(res.err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", res.err, tc.want)
			}
		})
	}
}

func TestFeaturesSyntaxDirective(t *testing.T) {
	withSyntax := docker.ExtractDockerfile("# syntax=docker/dockerfile:1.3\nFROM alpine\n")
	engine := func(v string) *docker.EngineClient { return &docker.EngineClient{API: &fakeDockerAPI{version: v}} }
	for name, tc := range map[string]struct {
		engine     *docker.EngineClient
		omit, ctxs bool
		parsed     *docker.Dockerfile
		want       string
	}{
		"omitted":                     {engine("25.0.0"), true, true, withSyntax, ""},
		"engine 23 keeps the user's":  {engine("23.0.0"), false, true, withSyntax, "docker/dockerfile:1.3"},
		"old engine needs 1.4":        {engine("20.10.7"), false, true, withSyntax, "docker/dockerfile:1.4"},
		"old engine without contexts": {engine("20.10.7"), false, false, withSyntax, "docker/dockerfile:1.3"},
		"no Dockerfile, old engine":   {engine("20.10.7"), false, true, nil, "docker/dockerfile:1.4"},
		"no Dockerfile, no contexts":  {engine("20.10.7"), false, false, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := featuresSyntaxDirective(t.Context(), tc.engine, tc.omit, tc.ctxs, tc.parsed); got != tc.want {
				t.Errorf("syntax = %q, want %q", got, tc.want)
			}
		})
	}
}
