package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A compose entrypoint/command string is split like shell-quote parses it.
func TestSplitShellWords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a  b\tc\nd", []string{"a", "b", "c", "d"}},
		{`'a b' c`, []string{"a b", "c"}},
		{`"ab" cd`, []string{"ab", "cd"}},
		{`"ab"cd`, []string{"abcd"}},
		{`"" x`, []string{"", "x"}},
		{`"x \"y\" z"`, []string{`x "y" z`}},
		{`'a\b'`, []string{`a\b`}},
		{`a\ b`, []string{"a b"}},
		{`"x\y`, []string{"xy"}},
		{`"a\`, []string{`a\`}},
		{`a\`, []string{`a\`}},
	}
	for _, tt := range tests {
		if got := splitShellWords(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("splitShellWords(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestComposeStringList(t *testing.T) {
	if got := composeStringList([]any{"a", 1}); !slices.Equal(got, []string{"a", "1"}) {
		t.Errorf("list: %q", got)
	}
	if got := composeStringList("sh -c 'echo hi'"); !slices.Equal(got, []string{"sh", "-c", "echo hi"}) {
		t.Errorf("string: %q", got)
	}
	if got := composeStringList(nil); got != nil {
		t.Errorf("unset: %q, want nil", got)
	}
}

func TestSortedStringKeys(t *testing.T) {
	if got := sortedStringKeys(map[string]string{"b": "2", "a": "1"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("sortedStringKeys = %q", got)
	}
}

// TS supportsBuildContexts: docker/dockerfile >= 1.4, an unversioned or
// non-numeric tag, or a frontend that is not docker/dockerfile.
func TestDockerfileSupportsBuildContexts(t *testing.T) {
	df := func(version, syntax string) *docker.Dockerfile {
		return &docker.Dockerfile{Preamble: docker.Preamble{Version: version, Directives: map[string]string{"syntax": syntax}}}
	}
	tests := []struct {
		name string
		df   *docker.Dockerfile
		want bool
	}{
		{"no Dockerfile", nil, false},
		{"no syntax", df("", ""), false},
		{"other frontend", df("", "example.com/frontend"), true},
		{"labs", df("labs", "docker/dockerfile:labs"), true},
		{"1", df("1", "docker/dockerfile:1"), true},
		{"1.3", df("1.3", "docker/dockerfile:1.3"), false},
		{"1.3-labs", df("1.3-labs", "docker/dockerfile:1.3-labs"), false},
		{"1.4", df("1.4", "docker/dockerfile:1.4"), true},
		{"1.4.0", df("1.4.0", "docker/dockerfile:1.4.0"), true},
		{"1.10", df("1.10", "docker/dockerfile:1.10"), true},
		{"2", df("2", "docker/dockerfile:2"), true},
		{"2.0", df("2.0", "docker/dockerfile:2.0"), true},
		{"0.9", df("0.9", "docker/dockerfile:0.9"), false},
	}
	for _, tt := range tests {
		if got := dockerfileSupportsBuildContexts(tt.df); got != tt.want {
			t.Errorf("%s: dockerfileSupportsBuildContexts = %t, want %t", tt.name, got, tt.want)
		}
	}
}

// The override file name carries a random version 4 UUID.
func TestNewRandomUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 64 {
		u := newRandomUUID()
		if !re.MatchString(u) {
			t.Fatalf("newRandomUUID() = %q, not a version 4 UUID", u)
		}
		seen[u] = true
	}
	if len(seen) != 64 {
		t.Errorf("64 UUIDs, %d distinct", len(seen))
	}
}

// TS checkForPersistedFile: the label names the file, and it is restored only
// when it is still a regular file.
func TestCheckPersistedComposeFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, composeBuildOverridePrefix+"-1.yml")
	if err := os.WriteFile(file, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(dir, composeFeaturesOverridePrefix+"-2.yml")
	if err := os.Mkdir(asDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &upRunner{log: log.New(log.Options{Writer: io.Discard})}
	other := filepath.Join(dir, "docker-compose.yml")
	tests := []struct {
		name   string
		files  []string
		prefix string
		want   persistedComposeFile
	}{
		{"present", []string{other, file}, composeBuildOverridePrefix, persistedComposeFile{foundLabel: true, fileExists: true, file: file}},
		{"a directory", []string{other, asDir}, composeFeaturesOverridePrefix, persistedComposeFile{foundLabel: true, file: asDir}},
		{"gone", []string{filepath.Join(dir, composeFeaturesOverridePrefix+"-3.yml")}, composeFeaturesOverridePrefix, persistedComposeFile{foundLabel: true, file: filepath.Join(dir, composeFeaturesOverridePrefix+"-3.yml")}},
		{"not in the label", []string{other}, composeBuildOverridePrefix, persistedComposeFile{}},
	}
	for _, tt := range tests {
		if got := r.checkPersistedComposeFile(tt.files, tt.prefix); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// composeFakeEngine is the Docker Engine API the compose flow talks to: images
// present locally, images a pull brings in, the daemon version and the
// containers a label lookup finds. A call to any other API method panics.
type composeFakeEngine struct {
	docker.API
	images     map[string]image.InspectResponse
	registry   map[string]image.InspectResponse
	inspects   []string
	pulls      []string
	version    string
	containers []string
	listErr    error
}

func (f *composeFakeEngine) ImageInspect(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
	f.inspects = append(f.inspects, ref)
	if img, ok := f.images[ref]; ok {
		return mobyclient.ImageInspectResult{InspectResponse: img}, nil
	}
	return mobyclient.ImageInspectResult{}, errors.New("No such image: " + ref)
}

func (f *composeFakeEngine) ImagePull(_ context.Context, ref string, _ mobyclient.ImagePullOptions) (mobyclient.ImagePullResponse, error) {
	f.pulls = append(f.pulls, ref)
	img, ok := f.registry[ref]
	if !ok {
		return nil, errors.New("pull access denied for " + ref)
	}
	f.images[ref] = img
	return composeFakePull{}, nil
}

func (f *composeFakeEngine) ServerVersion(context.Context, mobyclient.ServerVersionOptions) (mobyclient.ServerVersionResult, error) {
	return mobyclient.ServerVersionResult{Version: f.version}, nil
}

func (f *composeFakeEngine) ContainerList(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	var res mobyclient.ContainerListResult
	for _, id := range f.containers {
		res.Items = append(res.Items, container.Summary{ID: id})
	}
	return res, f.listErr
}

type composeFakePull struct{ mobyclient.ImagePullResponse }

func (composeFakePull) Read([]byte) (int, error) { return 0, io.EOF }
func (composeFakePull) Close() error             { return nil }

// composeFakeRunner records the docker and docker compose processes the flow
// starts; one whose command line contains failOn exits 1.
type composeFakeRunner struct {
	calls  [][]string
	failOn string
}

func (f *composeFakeRunner) Run(_ context.Context, _ io.Writer, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.failOn != "" && strings.Contains(strings.Join(args, " "), f.failOn) {
		return nil, []byte("failed"), 1, nil
	}
	return nil, nil, 0, nil
}

// compose returns the arguments of each `docker compose ... <sub> ...` run.
func (f *composeFakeRunner) compose(sub string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == "compose" && slices.Contains(c, sub) {
			out = append(out, c[2:])
		}
	}
	return out
}

// dockerBuilds returns the arguments of each `docker build` run.
func (f *composeFakeRunner) dockerBuilds() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 1 && c[1] != "compose" && slices.Contains(c, "build") {
			out = append(out, c[1:])
		}
	}
	return out
}

// composeHarness is a compose project on disk (.devcontainer with a compose
// file) and an upRunner whose docker, compose and engine are fakes.
type composeHarness struct {
	t         *testing.T
	r         *upRunner
	engine    *composeFakeEngine
	run       *composeFakeRunner
	logs      *bytes.Buffer
	ws, dc    string
	compose   string
	persisted string
}

const composeTestBaseMetadata = `[{"remoteUser":"node"}]`

func newComposeHarness(t *testing.T) *composeHarness {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	ws := t.TempDir()
	dc := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(dc, 0o755); err != nil {
		t.Fatal(err)
	}
	compose := filepath.Join(dc, "docker-compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  app:\n    image: alpine:3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	logger := log.New(log.Options{Level: log.LevelTrace, Format: "text", Writer: logs})
	engine := &composeFakeEngine{
		images: map[string]image.InspectResponse{
			"alpine:3": {
				Os: "linux", Architecture: "amd64",
				Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{
					Labels:     map[string]string{imagemeta.MetadataLabel: composeTestBaseMetadata},
					Entrypoint: []string{"docker-entrypoint.sh"},
					Cmd:        []string{"sh"},
				}},
			},
		},
		registry:   map[string]image.InspectResponse{},
		version:    "24.0.0",
		containers: []string{"cid1"},
	}
	run := &composeFakeRunner{}
	persisted := filepath.Join(t.TempDir(), "persisted")
	return &composeHarness{
		t:  t,
		ws: ws, dc: dc, compose: compose, persisted: persisted,
		engine: engine, run: run, logs: logs,
		r: &upRunner{
			log:    logger,
			docker: &docker.Client{DockerPath: "docker", Log: logger, Runner: run},
			engine: &docker.EngineClient{API: engine, Log: logger},
			opts:   &upOpts{userDataFolder: persisted, updateRemoteUserUIDDefault: "off", gpuAvailability: "none"},
		},
	}
}

// start is the composeStart for service app of the given compose config.
func (h *composeHarness) start(cfg *config.DevContainer, svc map[string]any) composeStart {
	if cfg.Service == "" {
		cfg.Service = "app"
	}
	if cfg.ConfigFilePath == "" {
		cfg.ConfigFilePath = filepath.Join(h.dc, "devcontainer.json")
	}
	return composeStart{
		cfg:             cfg,
		loadResult:      &config.LoadResult{},
		workspaceFolder: h.ws,
		idLabels:        []string{"devcontainer.local_folder=" + h.ws},
		projectName:     "proj",
		composeFiles:    []string{h.compose},
		composeConfig:   map[string]any{"services": map[string]any{cfg.Service: svc}},
		client:          &docker.ComposeClient{Command: []string{"docker", "compose"}, Version: []int{2, 20, 0}, Log: h.r.log, Runner: h.run},
	}
}

func (h *composeHarness) write(rel, content string) string {
	h.t.Helper()
	p := filepath.Join(h.dc, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// localFeature writes the Feature ./hello next to devcontainer.json.
func (h *composeHarness) localFeature() map[string]any {
	h.write("hello/devcontainer-feature.json", `{"id":"hello","version":"1.0.0","name":"Hello","containerEnv":{"HELLO":"${localEnv:HELLO_SRC}"}}`)
	h.write("hello/install.sh", "#!/bin/sh\n")
	return map[string]any{"./hello": map[string]any{}}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func overrideFiles(t *testing.T, persisted string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(persisted, "docker-compose", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestComposeServicesFilter(t *testing.T) {
	tests := []struct {
		run  []string
		want []string
	}{
		{nil, nil},
		{[]string{"db"}, []string{"db", "app"}},
		{[]string{"app", "db"}, []string{"app", "db"}},
	}
	for _, tt := range tests {
		cfg := &config.DevContainer{Service: "app", RunServices: tt.run}
		if got := composeServicesFilter(cfg); !slices.Equal(got, tt.want) {
			t.Errorf("runServices %q: %q, want %q", tt.run, got, tt.want)
		}
	}
}

// TS inspectDockerImage with pullImageOnError: a missing image is pulled once
// and inspected again; a failed pull is the error.
func TestInspectImagePulling(t *testing.T) {
	h := newComposeHarness(t)
	ctx := context.Background()
	if _, err := h.r.inspectImagePulling(ctx, "alpine:3"); err != nil || len(h.engine.pulls) != 0 {
		t.Fatalf("present image: err = %v, pulls = %q", err, h.engine.pulls)
	}
	h.engine.registry["node:22"] = image.InspectResponse{ID: "sha256:node"}
	info, err := h.r.inspectImagePulling(ctx, "node:22")
	if err != nil || info.ID != "sha256:node" || !slices.Equal(h.engine.pulls, []string{"node:22"}) {
		t.Fatalf("pulled image: %q, %v, pulls = %q", info.ID, err, h.engine.pulls)
	}
	if _, err := h.r.inspectImagePulling(ctx, "nowhere:1"); err == nil || !strings.Contains(err.Error(), "pull access denied for nowhere:1") {
		t.Errorf("unpullable image: err = %v", err)
	}
}

// TS updateRemoteUserUID: the image's user is the remote user when nothing
// names another, and the UID image is built for the image's platform.
func TestUpdateRemoteUserUIDImageUserAndPlatform(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("updateRemoteUserUID only runs on Linux")
	}
	tests := []struct {
		name     string
		details  image.InspectResponse
		want     string
		platform string
	}{
		{"image user", image.InspectResponse{Os: "linux", Architecture: "arm64", Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: "vscode"}}}, "-uid", "--platform linux/arm64 "},
		{"variant", image.InspectResponse{Os: "linux", Architecture: "arm", Variant: "v7", Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: "vscode"}}}, "-uid", "--platform linux/arm/v7 "},
		{"image runs as root", image.InspectResponse{Os: "linux", Config: &dockerspec.DockerOCIImageConfig{}}, "", ""},
		{"no image config", image.InspectResponse{Os: "linux"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			h.r.opts.updateRemoteUserUIDDefault = "on"
			details := func() (image.InspectResponse, error) { return tt.details, nil }
			got, err := h.r.updateRemoteUserUID(context.Background(), &imagemeta.MergedConfig{}, "img", details, "", h.ws, h.persisted)
			if err != nil {
				t.Fatal(err)
			}
			builds := h.run.dockerBuilds()
			if tt.want == "" {
				if got != "img" || len(builds) != 0 {
					t.Errorf("got %q after %d builds, want img unchanged", got, len(builds))
				}
				return
			}
			if want := folderImageName(h.ws) + tt.want; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			if len(builds) != 1 {
				t.Fatalf("%d builds, want 1", len(builds))
			}
			line := strings.Join(builds[0], " ") + " "
			for _, want := range []string{tt.platform, "REMOTE_USER=vscode ", "IMAGE_USER=vscode "} {
				if !strings.Contains(line, want) {
					t.Errorf("build %q lacks %q", line, want)
				}
			}
		})
	}
}

// The container override: mounts with ${devcontainerId} resolved plus the
// --mount values, the image's entrypoint/command where compose sets none, the
// labels, and the GPU request.
func TestWriteComposeFeaturesOverride(t *testing.T) {
	ctx := context.Background()
	boolp := func(b bool) *bool { return &b }
	unexpected := func() (image.InspectResponse, error) {
		return image.InspectResponse{}, errors.New("image details read although compose sets entrypoint and command")
	}
	t.Run("mounts and image entrypoint", func(t *testing.T) {
		h := newComposeHarness(t)
		h.r.opts.mounts = []string{"type=bind,source=/host,target=/cli"}
		svc := map[string]any{"image": "alpine:3", "command": []any{"sleep", "infinity"}}
		s := h.start(&config.DevContainer{}, svc)
		merged := &imagemeta.MergedConfig{Mounts: []any{"type=volume,source=${devcontainerId}-data,target=/data"}}
		calls := 0
		details := func() (image.InspectResponse, error) { calls++; return h.engine.images["alpine:3"], nil }
		path, err := h.r.writeComposeFeaturesOverride(ctx, s, "alpine:3", "alpine:3", merged, "version: '3'\n\n", details, svc, []string{"a=b"}, h.persisted)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(path) != filepath.Join(h.persisted, "docker-compose") ||
			!regexp.MustCompile(`^docker-compose\.devcontainer\.containerFeatures-\d+-[0-9a-f-]{36}\.yml$`).MatchString(filepath.Base(path)) {
			t.Errorf("override path %q", path)
		}
		content := readFile(t, path)
		id := config.ComputeDevContainerID(config.EnvMap(s.idLabels))
		for _, want := range []string{
			"version: '3'\n\nservices:\n  'app':\n",
			`, "-", "docker-entrypoint.sh"]`,
			"\n    labels:\n      - 'a=b'",
			"\n    volumes:\n      - " + id + "-data:/data\n      - /host:/cli\n",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("override lacks %q:\n%s", want, content)
			}
		}
		if strings.Contains(content, "command:") || strings.Contains(content, "deploy:") {
			t.Errorf("override sets the compose command or a GPU:\n%s", content)
		}
		if calls != 1 {
			t.Errorf("image details read %d times, want 1", calls)
		}
	})
	tests := []struct {
		name    string
		svc     map[string]any
		merged  *imagemeta.MergedConfig
		details func() (image.InspectResponse, error)
		want    string
	}{
		{"image entrypoint and command", map[string]any{}, &imagemeta.MergedConfig{}, nil, `"-", "docker-entrypoint.sh"]` + "\n    command: [\"sh\"]\n"},
		{"compose entrypoint and command", map[string]any{"entrypoint": "/ep.sh", "command": "run"}, &imagemeta.MergedConfig{}, unexpected, `"-", "/ep.sh"]` + "\n"},
		{"overrideCommand", map[string]any{}, &imagemeta.MergedConfig{OverrideCommand: boolp(true)}, unexpected, `"-"]` + "\n    command: []\n"},
		{"image without config", map[string]any{}, &imagemeta.MergedConfig{}, func() (image.InspectResponse, error) { return image.InspectResponse{}, nil }, `"-"]` + "\n    command: []\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			details := tt.details
			if details == nil {
				details = func() (image.InspectResponse, error) { return h.engine.images["alpine:3"], nil }
			}
			s := h.start(&config.DevContainer{}, tt.svc)
			path, err := h.r.writeComposeFeaturesOverride(ctx, s, "img", "img", tt.merged, "", details, tt.svc, nil, h.persisted)
			if err != nil {
				t.Fatal(err)
			}
			if content := readFile(t, path); !strings.HasSuffix(content, tt.want) {
				t.Errorf("override does not end with %q:\n%s", tt.want, content)
			}
		})
	}
	t.Run("image details fail", func(t *testing.T) {
		h := newComposeHarness(t)
		s := h.start(&config.DevContainer{}, map[string]any{})
		failing := func() (image.InspectResponse, error) { return image.InspectResponse{}, errors.New("no such image") }
		if _, err := h.r.writeComposeFeaturesOverride(ctx, s, "img", "img", &imagemeta.MergedConfig{}, "", failing, map[string]any{}, nil, h.persisted); err == nil || err.Error() != "no such image" {
			t.Errorf("err = %v, want the image details error", err)
		}
	})
	t.Run("persisted folder unusable", func(t *testing.T) {
		h := newComposeHarness(t)
		if err := os.WriteFile(h.persisted, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		s := h.start(&config.DevContainer{}, map[string]any{})
		if _, err := h.r.writeComposeFeaturesOverride(ctx, s, "img", "img", &imagemeta.MergedConfig{OverrideCommand: boolp(true)}, "", unexpected, map[string]any{}, nil, h.persisted); err == nil || !strings.Contains(err.Error(), "create compose override folder") {
			t.Errorf("err = %v", err)
		}
	})
	gpus := []struct {
		name, gpu, availability string
		deploy, warning         bool
	}{
		{"no host requirements", "", "all", false, false},
		{"gpu false", "false", "all", false, false},
		{"gpu available", "true", "all", true, false},
		{"gpu required, none available", "true", "none", false, true},
		{"gpu optional, none available", `"optional"`, "none", false, false},
	}
	for _, tt := range gpus {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			h.r.opts.gpuAvailability = tt.availability
			cfg := &config.DevContainer{}
			if tt.gpu != "" {
				cfg.HostRequirements = &config.HostRequirements{GPU: json.RawMessage(tt.gpu)}
			}
			s := h.start(cfg, map[string]any{})
			path, err := h.r.writeComposeFeaturesOverride(ctx, s, "img", "img", &imagemeta.MergedConfig{OverrideCommand: boolp(true)}, "", unexpected, map[string]any{}, nil, h.persisted)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(readFile(t, path), "\n    deploy:\n"); got != tt.deploy {
				t.Errorf("deploy section = %t, want %t", got, tt.deploy)
			}
			if got := strings.Contains(h.logs.String(), "No GPU support found yet a GPU was required"); got != tt.warning {
				t.Errorf("GPU warning = %t, want %t", got, tt.warning)
			}
		})
	}
}

// featureTarball is a gzipped Feature tarball holding id.
func featureTarball(t *testing.T, id string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"devcontainer-feature.json": `{"id":"` + id + `","version":"1.0.0","name":"` + id + `"}`,
		"install.sh":                "#!/bin/sh\n",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The compose flow fetches Features like extendImageWithFeatures: host
// variables substituted, the lockfile read unless --no-lockfile and written
// only when asked.
func TestComposeFeatureSets(t *testing.T) {
	ctx := context.Background()
	lr := &config.LoadResult{HostSub: config.HostSubContext{Platform: "linux", Env: map[string]string{"HELLO_SRC": "from-host"}}}
	t.Run("no features", func(t *testing.T) {
		h := newComposeHarness(t)
		sets, dir, err := h.r.composeFeatureSets(ctx, &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json")}, lr)
		if sets != nil || dir != "" || err != nil {
			t.Errorf("got %v, %q, %v; want nothing", sets, dir, err)
		}
	})
	t.Run("local feature", func(t *testing.T) {
		h := newComposeHarness(t)
		cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: h.localFeature()}
		sets, dir, err := h.r.composeFeatureSets(ctx, cfg, lr)
		if err != nil {
			t.Fatal(err)
		}
		if len(sets) != 1 || len(sets[0].Features) != 1 || sets[0].Features[0].ID != "hello" {
			t.Fatalf("sets = %+v", sets)
		}
		if got := sets[0].Features[0].ContainerEnv["HELLO"]; got != "from-host" {
			t.Errorf("containerEnv HELLO = %q, want the host variable substituted", got)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("features folder %q: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(h.dc, "devcontainer-lock.json")); !os.IsNotExist(err) {
			t.Errorf("lockfile written without --experimental-lockfile: %v", err)
		}
	})
	t.Run("missing feature", func(t *testing.T) {
		h := newComposeHarness(t)
		cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: map[string]any{"./nope": map[string]any{}}}
		if _, _, err := h.r.composeFeatureSets(ctx, cfg, lr); err == nil {
			t.Error("want an error for a Feature folder that does not exist")
		}
	})
	t.Run("experimental lockfile", func(t *testing.T) {
		h := newComposeHarness(t)
		h.r.opts.experimentalLockfile = true
		cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: h.localFeature()}
		if _, _, err := h.r.composeFeatureSets(ctx, cfg, lr); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(h.dc, "devcontainer-lock.json")); err != nil {
			t.Errorf("lockfile not written: %v", err)
		}
	})
	t.Run("frozen lockfile out of date", func(t *testing.T) {
		h := newComposeHarness(t)
		h.r.opts.experimentalFrozenLockfile = true
		h.write("devcontainer-lock.json", `{"features":{"stale":{}}}`)
		cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: h.localFeature()}
		if _, _, err := h.r.composeFeatureSets(ctx, cfg, lr); err == nil || !strings.HasPrefix(err.Error(), "lockfile: ") {
			t.Errorf("err = %v, want the lockfile error", err)
		}
	})
	t.Run("existing lockfile left alone", func(t *testing.T) {
		h := newComposeHarness(t)
		lock := h.write("devcontainer-lock.json", `{"features":{"stale":{}}}`)
		cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: h.localFeature()}
		if _, _, err := h.r.composeFeatureSets(ctx, cfg, lr); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, lock); got != `{"features":{"stale":{}}}` {
			t.Errorf("lockfile rewritten without a lockfile flag: %s", got)
		}
	})
	t.Run("lockfile pins a tarball", func(t *testing.T) {
		tgz := featureTarball(t, "tarred")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(tgz) }))
		defer srv.Close()
		uri := srv.URL + "/devcontainer-feature-tarred.tgz"
		for _, noLockfile := range []bool{false, true} {
			h := newComposeHarness(t)
			h.r.opts.noLockfile = noLockfile
			h.write("devcontainer-lock.json", `{"features":{"`+uri+`":{"version":"1.0.0","resolved":"`+uri+`","integrity":"sha256:`+strings.Repeat("0", 64)+`"}}}`)
			cfg := &config.DevContainer{ConfigFilePath: filepath.Join(h.dc, "devcontainer.json"), Features: map[string]any{uri: map[string]any{}}}
			_, _, err := h.r.composeFeatureSets(ctx, cfg, lr)
			if mismatch := err != nil && strings.Contains(err.Error(), "Digest did not match"); mismatch == noLockfile {
				t.Errorf("noLockfile=%t: err = %v", noLockfile, err)
			}
		}
	})
}

// overrideDockerfile is the `dockerfile:` a build override sets.
func overrideDockerfile(t *testing.T, override string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^      dockerfile: (.*)$`).FindStringSubmatch(override)
	if m == nil {
		t.Fatalf("build override sets no dockerfile:\n%s", override)
	}
	return m[1]
}

// A Dockerfile service without Features: TS getImageBuildOptions extends the
// service's Dockerfile with the metadata label, and compose builds it.
func TestBuildAndExtendComposeDockerfileService(t *testing.T) {
	h := newComposeHarness(t)
	h.write("Dockerfile", "# syntax=docker/dockerfile:1.4\nFROM alpine:3\nRUN true\n")
	h.write("Dockerfile.dockerignore", "secret\n")
	// No context: the compose file's folder.
	s := h.start(&config.DevContainer{}, map[string]any{"build": map[string]any{}})
	s.useBuildx = true
	res, err := h.r.buildAndExtendCompose(context.Background(), s, h.persisted, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.overrideFiles) != 1 {
		t.Fatalf("override files = %q", res.overrideFiles)
	}
	override := readFile(t, res.overrideFiles[0])
	final := overrideDockerfile(t, override)
	if filepath.Base(final) != "Dockerfile-with-features" || !strings.HasPrefix(final, filepath.Join(cacheFolder(), "container-features")+string(filepath.Separator)) {
		t.Errorf("final Dockerfile %q", final)
	}
	if want := "services:\n  app:\n    build:\n      dockerfile: " + final + "\n      args:\n        - BUILDKIT_INLINE_CACHE=1\n        - _DEV_CONTAINERS_BASE_IMAGE=dev_container_auto_added_stage_label\n\n"; override != want {
		t.Errorf("build override\ngot:  %q\nwant: %q", override, want)
	}
	content := readFile(t, final)
	if want := "# syntax=docker/dockerfile:1.4\n    ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\nFROM alpine:3 AS dev_container_auto_added_stage_label\nRUN true\n\n\nFROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\nLABEL devcontainer.metadata="; !strings.HasPrefix(content, want) || strings.Count(content, "# syntax") != 1 {
		t.Errorf("final Dockerfile\ngot:  %q\nwant: %q...", content, want)
	}
	if got := readFile(t, final+".dockerignore"); got != "secret\n" {
		t.Errorf("sibling .dockerignore = %q", got)
	}
	if len(res.imageMetadata) != 2 || res.imageMetadata[0].RemoteUser != "node" {
		t.Errorf("image metadata = %+v, want the base image's then the config's", res.imageMetadata)
	}
	if res.labels != nil || res.overrideImageName != "" {
		t.Errorf("labels %q, override image %q; want none", res.labels, res.overrideImageName)
	}
	if got, want := h.run.compose("build"), [][]string{{"-f", h.compose, "-f", res.overrideFiles[0], "--project-name", "proj", "build"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("compose build runs %q, want %q", got, want)
	}
}

// The service's target is replaced by the extended stage, and its Dockerfile
// path resolves against the build context unless absolute.
func TestBuildAndExtendComposeTargetAndDockerfilePath(t *testing.T) {
	const df = "# syntax=docker/dockerfile:1.4\nFROM alpine:3 AS base\nFROM base AS dev\nFROM base AS other\n"
	for _, absolute := range []bool{false, true} {
		h := newComposeHarness(t)
		h.r.opts.omitSyntaxDirective = true
		path := h.write("sub/Dockerfile", df)
		build := map[string]any{"context": h.dc, "dockerfile": "sub/Dockerfile", "target": "dev"}
		if absolute {
			build = map[string]any{"context": h.ws, "dockerfile": path, "target": "dev"}
		}
		s := h.start(&config.DevContainer{}, map[string]any{"build": build})
		res, err := h.r.buildAndExtendCompose(context.Background(), s, h.persisted, "", true)
		if err != nil {
			t.Fatalf("absolute=%t: %v", absolute, err)
		}
		override := readFile(t, res.overrideFiles[0])
		for _, want := range []string{"      target: dev_containers_target_stage\n", "        - _DEV_CONTAINERS_BASE_IMAGE=dev\n"} {
			if !strings.Contains(override, want) {
				t.Errorf("absolute=%t: override lacks %q:\n%s", absolute, want, override)
			}
		}
		if strings.Contains(override, "BUILDKIT_INLINE_CACHE") {
			t.Errorf("absolute=%t: inline cache without buildx:\n%s", absolute, override)
		}
		content := readFile(t, overrideDockerfile(t, override))
		if !strings.HasPrefix(content, "\n    ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\nFROM alpine:3 AS base\nFROM base AS dev\nFROM base AS other\n") {
			t.Errorf("absolute=%t: final Dockerfile %q", absolute, content)
		}
		if !slices.Contains(h.engine.inspects, "alpine:3") || len(h.run.calls) != 0 {
			t.Errorf("absolute=%t: inspected %q, ran %q", absolute, h.engine.inspects, h.run.calls)
		}
	}
}

// An image service without Features gets the metadata label on the container,
// and a build override only for --cache-from.
func TestBuildAndExtendComposeImageService(t *testing.T) {
	ctx := context.Background()
	t.Run("label only", func(t *testing.T) {
		h := newComposeHarness(t)
		s := h.start(&config.DevContainer{}, map[string]any{"image": "alpine:3"})
		res, err := h.r.buildAndExtendCompose(ctx, s, h.persisted, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{imagemeta.MetadataLabel + "=" + imagemeta.GenerateMetadataLabel(res.imageMetadata)}; !slices.Equal(res.labels, want) {
			t.Errorf("labels = %q, want %q", res.labels, want)
		}
		if len(res.overrideFiles) != 0 || len(overrideFiles(t, h.persisted)) != 0 {
			t.Errorf("override files written: %q", res.overrideFiles)
		}
		if got, want := h.run.compose("build"), [][]string{{"-f", h.compose, "--project-name", "proj", "build"}}; !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("compose build runs %q, want %q", got, want)
		}
	})
	t.Run("cache-from, runServices, no cache", func(t *testing.T) {
		h := newComposeHarness(t)
		h.r.opts.cacheFrom = []string{"c1"}
		h.r.opts.buildNoCache = true
		s := h.start(&config.DevContainer{RunServices: []string{"db"}}, map[string]any{"image": "alpine:3"})
		res, err := h.r.buildAndExtendCompose(ctx, s, h.persisted, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.overrideFiles) != 1 || filepath.Dir(res.overrideFiles[0]) != filepath.Join(h.persisted, "docker-compose") {
			t.Fatalf("override files = %q", res.overrideFiles)
		}
		if got := readFile(t, res.overrideFiles[0]); got != "services:\n  app:\n\n      cache_from:\n        - c1\n\n" {
			t.Errorf("build override = %q", got)
		}
		if got, want := h.run.compose("build"), [][]string{{"-f", h.compose, "-f", res.overrideFiles[0], "--project-name", "proj", "build", "--no-cache", "db", "app"}}; !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("compose build runs %q, want %q", got, want)
		}
	})
	t.Run("no build", func(t *testing.T) {
		h := newComposeHarness(t)
		s := h.start(&config.DevContainer{}, map[string]any{"image": "alpine:3"})
		if _, err := h.r.buildAndExtendCompose(ctx, s, h.persisted, "", true); err != nil || len(h.run.calls) != 0 {
			t.Errorf("err = %v, ran %q", err, h.run.calls)
		}
	})
}

// An image service with Features is built by compose from an override that
// retags the folder image, gives an empty context and takes the Features'
// content from a build context or from the temp content image.
func TestBuildAndExtendComposeImageServiceWithFeatures(t *testing.T) {
	tests := []struct {
		name         string
		buildx       bool
		compose      []int
		engine       string
		omitSyntax   bool
		contexts     bool
		syntax       string
		contentImage bool
	}{
		{"no buildx", false, []int{2, 20, 0}, "20.10.0", false, false, "", true},
		{"build contexts, old engine", true, []int{2, 20, 0}, "20.10.0", false, true, "docker/dockerfile:1.4", false},
		{"build contexts, engine 23+", true, []int{2, 20, 0}, "24.0.0", false, true, "", false},
		{"build contexts, syntax omitted", true, []int{2, 20, 0}, "20.10.0", true, true, "", false},
		{"compose without build contexts", true, []int{2, 16, 0}, "20.10.0", false, false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			h.engine.version = tt.engine
			h.engine.images["alpine:3"].Config.User = "vscode"
			h.r.opts.omitSyntaxDirective = tt.omitSyntax
			s := h.start(&config.DevContainer{Features: h.localFeature()}, map[string]any{"image": "alpine:3"})
			s.useBuildx = tt.buildx
			s.client.Version = tt.compose
			res, err := h.r.buildAndExtendCompose(context.Background(), s, h.persisted, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if want := folderImageName(h.ws); res.overrideImageName != want {
				t.Errorf("override image = %q, want %q", res.overrideImageName, want)
			}
			if len(res.imageMetadata) != 3 {
				t.Errorf("image metadata = %+v, want the image's, the Feature's and the config's", res.imageMetadata)
			}
			override := readFile(t, res.overrideFiles[0])
			final := overrideDockerfile(t, override)
			featuresFolder := filepath.Dir(final)
			emptyDir := filepath.Join(h.persisted, "empty-folder")
			for _, want := range []string{
				"\n    image: " + folderImageName(h.ws) + "\n",
				"\n      context: " + emptyDir + "\n",
				"\n        - _DEV_CONTAINERS_IMAGE_USER=vscode\n",
			} {
				if !strings.Contains(override, want) {
					t.Errorf("override lacks %q:\n%s", want, override)
				}
			}
			if st, err := os.Stat(emptyDir); err != nil || !st.IsDir() {
				t.Errorf("empty context folder: %v", err)
			}
			wantContexts := "      additional_contexts:\n        - dev_containers_feature_content_source=" + featuresFolder + "\n"
			if got := strings.Contains(override, wantContexts); got != tt.contexts {
				t.Errorf("additional contexts = %t, want %t:\n%s", got, tt.contexts, override)
			}
			if got := strings.Contains(override, "BUILDKIT_INLINE_CACHE=1"); got != tt.buildx {
				t.Errorf("inline cache = %t, want %t", got, tt.buildx)
			}
			content := readFile(t, final)
			if !strings.Contains(content, "FROM alpine:3 AS dev_container_auto_added_stage_label\n") {
				t.Errorf("final Dockerfile lacks the service image stage:\n%s", content)
			}
			wantPrefix := "\nARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n"
			if tt.syntax != "" {
				wantPrefix = "# syntax=" + tt.syntax + wantPrefix
			}
			if !strings.HasPrefix(content, wantPrefix) {
				t.Errorf("final Dockerfile does not start with %q:\n%s", wantPrefix, content)
			}
			if got := len(h.run.dockerBuilds()) == 1; got != tt.contentImage {
				t.Errorf("content image built = %t, want %t (%q)", got, tt.contentImage, h.run.calls)
			}
		})
	}
}

// With build contexts, a Dockerfile service keeps its own syntax only when its
// frontend supports them (TS supportsBuildContexts).
func TestBuildAndExtendComposeDockerfileWithFeaturesSyntax(t *testing.T) {
	for user, want := range map[string]string{"docker/dockerfile:1.3": "docker/dockerfile:1.4", "docker/dockerfile:1.5": "docker/dockerfile:1.5"} {
		h := newComposeHarness(t)
		h.engine.version = "20.10.0"
		h.write("Dockerfile", "# syntax="+user+"\nFROM alpine:3\n")
		s := h.start(&config.DevContainer{Features: h.localFeature()}, map[string]any{"build": map[string]any{"context": h.dc, "target": ""}})
		s.useBuildx = true
		res, err := h.r.buildAndExtendCompose(context.Background(), s, h.persisted, "", true)
		if err != nil {
			t.Fatal(err)
		}
		override := readFile(t, res.overrideFiles[0])
		if strings.Contains(override, "image:") || strings.Contains(override, "context:") || res.overrideImageName != "" {
			t.Errorf("%s: a Dockerfile service keeps its image and context:\n%s", user, override)
		}
		content := readFile(t, overrideDockerfile(t, override))
		if !strings.HasPrefix(content, "# syntax="+want+"\n") || strings.Count(content, "# syntax") != 1 {
			t.Errorf("%s: final Dockerfile does not start with syntax %s:\n%s", user, want, content)
		}
	}
}

func TestBuildAndExtendComposeErrors(t *testing.T) {
	tests := []struct {
		name    string
		svc     map[string]any
		setup   func(h *composeHarness, cfg *config.DevContainer)
		wantErr string
	}{
		{"no Dockerfile", map[string]any{"build": map[string]any{}}, nil, "read Dockerfile for compose service"},
		{"neither image nor build", map[string]any{}, nil, `compose service "app" has neither an image nor a build`},
		{"image not pullable", map[string]any{"image": "nowhere:1"}, nil, "pull access denied for nowhere:1"},
		{"base image not pullable", map[string]any{"build": map[string]any{}}, func(h *composeHarness, _ *config.DevContainer) {
			h.write("Dockerfile", "FROM nowhere:1\n")
		}, "pull access denied for nowhere:1"},
		{"missing Feature", map[string]any{"image": "alpine:3"}, func(_ *composeHarness, cfg *config.DevContainer) {
			cfg.Features = map[string]any{"./nope": map[string]any{}}
		}, "nope"},
		{"compose build fails", map[string]any{"image": "alpine:3"}, func(h *composeHarness, _ *config.DevContainer) {
			h.run.failOn = "build"
		}, "An error occurred building the Docker Compose images"},
		{"content image fails", map[string]any{"image": "alpine:3"}, func(h *composeHarness, cfg *config.DevContainer) {
			cfg.Features = h.localFeature()
			h.run.failOn = "Dockerfile.buildContent"
		}, "build feature content image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			cfg := &config.DevContainer{}
			if tt.setup != nil {
				tt.setup(h, cfg)
			}
			s := h.start(cfg, tt.svc)
			if _, err := h.r.buildAndExtendCompose(context.Background(), s, h.persisted, "", false); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// A new container: compose builds the service, the container override is
// written and `compose up` creates the project with it.
func TestStartComposeContainerCreates(t *testing.T) {
	h := newComposeHarness(t)
	svc := map[string]any{"image": "alpine:3", "command": []any{"sleep", "infinity"}}
	id, err := h.r.startComposeContainer(context.Background(), h.start(&config.DevContainer{}, svc))
	if err != nil || id != "cid1" {
		t.Fatalf("got %q, %v", id, err)
	}
	if n := len(h.run.compose("build")); n != 1 {
		t.Errorf("%d compose builds, want 1", n)
	}
	files := overrideFiles(t, h.persisted)
	if len(files) != 1 || !strings.HasPrefix(filepath.Base(files[0]), composeFeaturesOverridePrefix) {
		t.Fatalf("override files = %q", files)
	}
	if got, want := h.run.compose("up"), [][]string{{"-f", h.compose, "-f", files[0], "--project-name", "proj", "up", "-d"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("compose up runs %q, want %q", got, want)
	}
	override := readFile(t, files[0])
	for _, want := range []string{`"-", "docker-entrypoint.sh"]`, "\n      - 'devcontainer.local_folder=" + h.ws + "'\n      - 'devcontainer.metadata="} {
		if !strings.Contains(override, want) {
			t.Errorf("override lacks %q:\n%s", want, override)
		}
	}
	if strings.Contains(override, "image:") {
		t.Errorf("override retags an unchanged image:\n%s", override)
	}
	if !strings.Contains(h.logs.String(), "ContainerHasLabels=false") {
		t.Errorf("log lacks ContainerHasLabels=false:\n%s", h.logs.String())
	}
}

// A stopped container is started with the override files it was created with
// when they are still there; otherwise they are written again, without a build.
func TestStartComposeContainerRestoresOverrides(t *testing.T) {
	tests := []struct {
		name      string
		build     string // "": not in the label, "gone": in the label but deleted
		start     string
		config    bool
		labels    bool
		restored  bool
		hasLabels bool
	}{
		{"both present", "present", "present", true, true, true, true},
		{"no build override", "", "present", true, true, true, true},
		{"build override gone", "gone", "present", true, true, false, true},
		{"container override gone", "present", "gone", true, true, false, true},
		{"no container override", "present", "", true, true, false, true},
		{"no labels", "present", "present", true, false, false, false},
		{"no config", "present", "present", false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			h.r.opts.updateRemoteUserUIDDefault = "on"
			dir := filepath.Join(h.persisted, "docker-compose")
			label := []string{h.compose}
			var want []string
			for _, f := range []struct{ state, prefix string }{{tt.build, composeBuildOverridePrefix}, {tt.start, composeFeaturesOverridePrefix}} {
				if f.state == "" {
					continue
				}
				path := filepath.Join(dir, f.prefix+"-1.yml")
				label = append(label, path)
				if f.state == "present" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					want = append(want, path)
				}
			}
			before := overrideFiles(t, h.persisted)
			s := h.start(&config.DevContainer{}, map[string]any{"image": "alpine:3", "entrypoint": "/ep.sh", "command": "run"})
			s.container = &container.InspectResponse{}
			if tt.config {
				s.container.Config = &container.Config{}
				if tt.labels {
					s.container.Config.Labels = map[string]string{"com.docker.compose.project.config_files": strings.Join(label, ",")}
				}
			}
			if _, err := h.r.startComposeContainer(context.Background(), s); err != nil {
				t.Fatal(err)
			}
			ups := h.run.compose("up")
			if len(ups) != 1 {
				t.Fatalf("compose up runs %q", ups)
			}
			up := ups[0]
			if len(h.run.compose("build")) != 0 || len(h.run.dockerBuilds()) != 0 {
				t.Errorf("an existing container's image was built: %q", h.run.calls)
			}
			var written []string
			for _, f := range overrideFiles(t, h.persisted) {
				if !slices.Contains(before, f) {
					written = append(written, f)
				}
			}
			files := want
			if !tt.restored {
				files = written
				if len(written) != 1 {
					t.Errorf("override files written: %q, want the container override again", written)
				}
			} else if len(written) != 0 {
				t.Errorf("override files written: %q, want the persisted ones restored", written)
			}
			wantUp := []string{"-f", h.compose}
			for _, f := range files {
				wantUp = append(wantUp, "-f", f)
			}
			wantUp = append(wantUp, "--project-name", "proj", "up", "-d", "--no-recreate")
			if !slices.Equal(up, wantUp) {
				t.Errorf("compose up %q, want %q", up, wantUp)
			}
			if want := "ContainerHasLabels=" + map[bool]string{true: "true", false: "false"}[tt.hasLabels]; !strings.Contains(h.logs.String(), want) {
				t.Errorf("log lacks %s", want)
			}
		})
	}
}

func TestStartComposeContainerUpOptions(t *testing.T) {
	tests := []struct {
		name     string
		run      []string
		existing bool
		tail     []string
	}{
		{"runServices", []string{"db"}, false, []string{"up", "-d", "db", "app"}},
		{"expected existing container", nil, true, []string{"up", "-d", "--no-recreate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			h.r.opts.expectExistingContainer = tt.existing
			s := h.start(&config.DevContainer{RunServices: tt.run}, map[string]any{"image": "alpine:3", "entrypoint": "/ep.sh", "command": "run"})
			if _, err := h.r.startComposeContainer(context.Background(), s); err != nil {
				t.Fatal(err)
			}
			ups := h.run.compose("up")
			if len(ups) != 1 || !slices.Equal(ups[0][len(ups[0])-len(tt.tail):], tt.tail) {
				t.Errorf("compose up runs %q, want one ending in %q", ups, tt.tail)
			}
		})
	}
}

func TestStartComposeContainerErrors(t *testing.T) {
	tests := []struct {
		name    string
		svc     map[string]any
		setup   func(h *composeHarness, s *composeStart)
		wantErr string
	}{
		{"compose up fails", nil, func(h *composeHarness, _ *composeStart) { h.run.failOn = "up -d" }, "compose up: compose up failed (exit 1)"},
		{"no container", nil, func(h *composeHarness, _ *composeStart) { h.engine.containers = nil }, `could not find container for service "app" in project "proj"`},
		{"container lookup fails", nil, func(h *composeHarness, _ *composeStart) { h.engine.listErr = errors.New("daemon gone") }, `could not find container for service "app" in project "proj"`},
		{"compose file unreadable", nil, func(h *composeHarness, s *composeStart) {
			s.composeFiles = []string{filepath.Join(h.dc, "missing.yml")}
		}, "read compose file"},
		{"image not pullable", map[string]any{"image": "nowhere:1", "entrypoint": "/ep.sh", "command": "run"}, nil, "pull access denied for nowhere:1"},
		{"built image missing", map[string]any{"build": map[string]any{}}, func(h *composeHarness, _ *composeStart) {
			h.write("Dockerfile", "FROM alpine:3\n")
		}, "pull access denied for proj-app"},
		{"persisted folder unusable", nil, func(h *composeHarness, _ *composeStart) {
			if err := os.WriteFile(h.persisted, nil, 0o644); err != nil {
				h.t.Fatal(err)
			}
		}, "create compose override folder"},
	}
	if runtime.GOOS == "linux" {
		tests = append(tests, struct {
			name    string
			svc     map[string]any
			setup   func(h *composeHarness, s *composeStart)
			wantErr string
		}{"UID image build fails", map[string]any{"build": map[string]any{}}, func(h *composeHarness, _ *composeStart) {
			h.write("Dockerfile", "FROM alpine:3\n")
			h.engine.images["proj-app"] = image.InspectResponse{Os: "linux"}
			h.r.opts.updateRemoteUserUIDDefault = "on"
			h.run.failOn = "updateUID"
		}, "updateUID build failed"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newComposeHarness(t)
			svc := tt.svc
			if svc == nil {
				svc = map[string]any{"image": "alpine:3", "entrypoint": "/ep.sh", "command": "run"}
			}
			s := h.start(&config.DevContainer{}, svc)
			if tt.setup != nil {
				tt.setup(h, &s)
			}
			if id, err := h.r.startComposeContainer(context.Background(), s); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got %q, %v; want an error with %q", id, err, tt.wantErr)
			}
		})
	}
}

// A service compose builds runs the image compose names it (TS
// getDefaultImageName), with the remote user's UID updated on top: the
// override retags the service to the UID image.
func TestStartComposeContainerBuildServiceUpdatesUID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("updateRemoteUserUID only runs on Linux")
	}
	h := newComposeHarness(t)
	h.r.opts.updateRemoteUserUIDDefault = "on"
	h.write("Dockerfile", "FROM alpine:3\n")
	h.engine.images["proj-app"] = image.InspectResponse{Os: "linux", Architecture: "amd64", Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Cmd: []string{"sh"}}}}
	if _, err := h.r.startComposeContainer(context.Background(), h.start(&config.DevContainer{}, map[string]any{"build": map[string]any{}})); err != nil {
		t.Fatal(err)
	}
	files := overrideFiles(t, h.persisted)
	var override string
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), composeFeaturesOverridePrefix) {
			override = readFile(t, f)
		}
	}
	fixed := folderImageName(h.ws) + "-uid"
	if !strings.Contains(override, "\n    image: "+fixed+"\n") || !strings.Contains(override, "\n    command: [\"sh\"]\n") {
		t.Errorf("container override does not run %s with the image's command:\n%s", fixed, override)
	}
	inspected := 0
	for _, ref := range h.engine.inspects {
		if ref == "proj-app" {
			inspected++
		}
	}
	if inspected != 1 {
		t.Errorf("built image inspected %d times, want once (%q)", inspected, h.engine.inspects)
	}
	if builds := h.run.dockerBuilds(); len(builds) != 1 || !strings.Contains(strings.Join(builds[0], " "), "BASE_IMAGE=proj-app") {
		t.Errorf("docker builds %q, want the UID image on proj-app", builds)
	}
}

// An image service with Features runs the folder image compose built, so its
// entrypoint is the one the container override wraps.
func TestStartComposeContainerImageServiceWithFeatures(t *testing.T) {
	h := newComposeHarness(t)
	folder := folderImageName(h.ws)
	h.engine.images[folder] = image.InspectResponse{Os: "linux", Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Entrypoint: []string{"/feature-ep.sh"}}}}
	s := h.start(&config.DevContainer{Features: h.localFeature()}, map[string]any{"image": "alpine:3", "command": "run"})
	if _, err := h.r.startComposeContainer(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var override string
	for _, f := range overrideFiles(t, h.persisted) {
		if strings.HasPrefix(filepath.Base(f), composeFeaturesOverridePrefix) {
			override = readFile(t, f)
		}
	}
	// The build override already tags the service with the folder image.
	if !strings.Contains(override, `"-", "/feature-ep.sh"]`) || strings.Contains(override, "image:") {
		t.Errorf("container override does not wrap the folder image's entrypoint:\n%s", override)
	}
	if ups := h.run.compose("up"); len(ups) != 1 || len(ups[0]) != 10 {
		t.Errorf("compose up %q, want the compose file and both overrides", ups)
	}
}
