package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func discardLogger() log.Logger { return log.New(log.Options{Writer: io.Discard}) }

// The deprecated --experimental-frozen-lockfile and --frozen-lockfile both
// make the Feature build verify the lockfile, as TS effectiveFrozenLockfile.
func TestBuildFrozenLockfileReachesTheFeatureBuild(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		frozen, experimental bool
		want                 bool
	}{
		{"neither", false, false, false},
		{"frozen-lockfile", true, false, true},
		{"experimental-frozen-lockfile", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &buildOpts{frozenLockfile: tc.frozen, experimentalFrozenLockfile: tc.experimental}
			opts.foldFrozenLockfile()
			got := (&buildRunner{opts: opts}).featureBuildOptions(&config.DevContainer{}, nil).FrozenLockfile
			if got != tc.want {
				t.Errorf("FrozenLockfile = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildDockerClientStreamsProgressOnlyInTextMode(t *testing.T) {
	if w := newBuildDockerClient("", "text", discardLogger()).ProgressWriter; w != os.Stderr {
		t.Errorf("text mode ProgressWriter = %v, want os.Stderr", w)
	}
	if w := newBuildDockerClient("", "json", discardLogger()).ProgressWriter; w != nil {
		t.Errorf("json mode ProgressWriter = %v, want nil (a raw stream corrupts JSON events)", w)
	}
}

func TestBuildFlagConflict(t *testing.T) {
	const (
		needsBuildKit   = "--platform or --push require BuildKit enabled."
		pushWithOutput  = "--push true cannot be used with --output."
		composePlatform = "--platform or --push not supported."
		composeOutput   = "--output not supported."
		composeCacheTo  = "--cache-to not supported."
	)
	for _, tc := range []struct {
		name      string
		opts      buildOpts
		useBuildx bool
		compose   bool
		want      string
	}{
		{"no flags", buildOpts{}, false, false, ""},
		{"platform without buildkit", buildOpts{platform: "linux/amd64"}, false, false, needsBuildKit},
		{"push without buildkit", buildOpts{push: true}, false, false, needsBuildKit},
		{"platform with buildkit", buildOpts{platform: "linux/amd64"}, true, false, ""},
		{"push with buildkit", buildOpts{push: true}, true, false, ""},
		{"output without buildkit", buildOpts{output: "type=oci,dest=x.tar"}, false, false, ""},
		{"push and output", buildOpts{push: true, output: "type=oci,dest=x.tar"}, true, false, pushWithOutput},
		{"cache-to on a single container", buildOpts{cacheTo: "type=local,dest=x"}, false, false, ""},
		{"compose without flags", buildOpts{}, true, true, ""},
		{"compose platform", buildOpts{platform: "linux/amd64"}, true, true, composePlatform},
		{"compose push", buildOpts{push: true}, true, true, composePlatform},
		{"compose output", buildOpts{output: "type=local,dest=x"}, false, true, composeOutput},
		{"compose cache-to", buildOpts{cacheTo: "type=local,dest=x"}, false, true, composeCacheTo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildFlagConflict(&tc.opts, tc.useBuildx, tc.compose); got != tc.want {
				t.Errorf("buildFlagConflict = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageBuildFor(t *testing.T) {
	oneFeature := map[string]any{"ghcr.io/devcontainers/features/go:1": map[string]any{}}
	for _, tc := range []struct {
		name     string
		features map[string]any
		opts     buildOpts
		want     imageBuild
	}{
		{"plain image", nil, buildOpts{}, imageAsIs},
		{"features win over platform", oneFeature, buildOpts{platform: "linux/amd64", imageNames: []string{"a"}}, imageWithFeatures},
		{"platform", nil, buildOpts{platform: "linux/amd64"}, imageExported},
		{"push", nil, buildOpts{push: true}, imageExported},
		{"output wins over image name", nil, buildOpts{output: "type=oci,dest=x.tar", imageNames: []string{"a"}}, imageExported},
		{"image name", nil, buildOpts{imageNames: []string{"a"}}, imageRelabeled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageBuildFor(&config.DevContainer{Features: tc.features}, &tc.opts); got != tc.want {
				t.Errorf("imageBuildFor = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCheckLegacyFeatures(t *testing.T) {
	for _, id := range []string{"node", "node:1", "golang", "ghcr.io/devcontainers/features/node:1", "./local-feature", "owner/repo/feature"} {
		if err := checkLegacyFeatures(map[string]any{id: true}); err != nil {
			t.Errorf("%s: unexpected error %v", id, err)
		}
	}
	for _, id := range []string{"not-a-feature", "not-a-feature:1"} {
		err := checkLegacyFeatures(map[string]any{id: true})
		if err == nil || !strings.Contains(err.Error(), "Legacy feature '"+id+"' not supported") {
			t.Errorf("%s: error = %v, want the legacy feature error naming it", id, err)
		}
	}
}

func TestImageNamesOr(t *testing.T) {
	if got := imageNamesOr(nil, "vsc-x"); !slices.Equal(got, []string{"vsc-x"}) {
		t.Errorf("no names: %v, want the fallback", got)
	}
	for _, names := range [][]string{{"a"}, {"a", "b"}} {
		if got := imageNamesOr(names, "vsc-x"); !slices.Equal(got, names) {
			t.Errorf("%v: got %v, want the given names", names, got)
		}
	}
}

func TestDockerfileImageMetadata(t *testing.T) {
	base := []imagemeta.Entry{{ID: "base"}}
	stage := []imagemeta.Entry{{ID: "stage"}}
	ids := func(entries []imagemeta.Entry) []string {
		var out []string
		for _, e := range entries {
			out = append(out, e.ID+"/"+e.RemoteUser)
		}
		return out
	}

	cfg := &config.DevContainer{RemoteUser: "dev"}
	configLabel := imagemeta.Entry{RemoteUser: "${localEnv:USER}"}
	if got, want := ids(dockerfileImageMetadata(cfg, base, stage, configLabel)), []string{"base/", "stage/", "/${localEnv:USER}"}; !slices.Equal(got, want) {
		t.Errorf("without features: %v, want %v (the config's entry as written)", got, want)
	}

	cfg.Features = map[string]any{"ghcr.io/devcontainers/features/go:1": true}
	if got, want := ids(dockerfileImageMetadata(cfg, base, stage, configLabel)), []string{"base/", "stage/"}; !slices.Equal(got, want) {
		t.Errorf("with features (the Feature build adds the config entry): %v, want %v", got, want)
	}
}

func TestDockerfileBaseExport(t *testing.T) {
	opts := &buildOpts{output: "type=oci,dest=x.tar", push: true}
	if output, push := dockerfileBaseExport(opts, &config.DevContainer{}); output != opts.output || !push {
		t.Errorf("without features: (%q, %v), want the flags", output, push)
	}
	withFeature := &config.DevContainer{Features: map[string]any{"ghcr.io/devcontainers/features/go:1": true}}
	if output, push := dockerfileBaseExport(opts, withFeature); output != "" || push {
		t.Errorf("with features: (%q, %v), want them left to the Feature build", output, push)
	}
}

func TestBuildxSecrets(t *testing.T) {
	secrets := []string{"TOKEN=x"}
	var logged bytes.Buffer
	logger := log.New(log.Options{Writer: &logged, Level: log.LevelTrace})

	if got := buildxSecrets(secrets, true, logger); !slices.Equal(got, secrets) {
		t.Errorf("buildx: %v, want %v", got, secrets)
	}
	if got := buildxSecrets(nil, false, logger); got != nil || logged.Len() != 0 {
		t.Errorf("legacy builder, no secrets: %v, logged %q; want nothing", got, logged.String())
	}
	if got := buildxSecrets(secrets, false, logger); got != nil {
		t.Errorf("legacy builder: %v, want the secrets dropped", got)
	}
	if !strings.Contains(logged.String(), "--secrets-file requires BuildKit") {
		t.Errorf("log = %q, want the dropped-secrets warning", logged.String())
	}
}

// --buildkit never skips BuildKit detection, so the build runs as if buildx
// were absent even when the Docker CLI has it. The fake --docker-path reports
// buildx, and both cases stop at the flag check before any Docker build.
func TestRunBuildBuildKitNeverIgnoresBuildx(t *testing.T) {
	bin := t.TempDir()
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho 'github.com/docker/buildx v0.20.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".devcontainer.json"), []byte(`{"image":"alpine:3"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		buildkit string
		want     string
	}{
		{"auto", "--push true cannot be used with --output."},
		{"never", "--platform or --push require BuildKit enabled."},
	} {
		t.Run(tc.buildkit, func(t *testing.T) {
			out := &captureOutput{}
			err := runBuild(t.Context(), out, &buildOpts{
				workspaceFolder: ws,
				dockerPath:      docker,
				logLevel:        "info",
				logFormat:       "json",
				buildkit:        tc.buildkit,
				push:            true,
				output:          "type=oci,dest=out.tar",
			})
			asExitCode(t, err)
			if !strings.Contains(out.out.String(), tc.want) {
				t.Errorf("stdout = %q, want %q", out.out.String(), tc.want)
			}
		})
	}
}

// The Dockerfile build targets the config's target when it names one, else
// the final stage, named when it has no name; a Dockerfile without FROM is
// TS's error.
func TestPrepareDockerfileBuildStage(t *testing.T) {
	ws := writeConfigFixture(t, map[string]string{
		".devcontainer/Dockerfile": "FROM alpine:3 AS base\nFROM base\n",
		".devcontainer/Empty":      "RUN true\n",
	})
	cfgFile := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	prep, err := prepareDockerfileBuild(&config.DevContainer{ConfigFilePath: cfgFile, Build: &config.Build{Dockerfile: "Dockerfile", Target: "base"}})
	if err != nil || prep.StageName != "base" {
		t.Fatalf("with a target: %+v, %v; want stage base", prep, err)
	}
	prep.Cleanup()
	prep, err = prepareDockerfileBuild(&config.DevContainer{ConfigFilePath: cfgFile, Build: &config.Build{Dockerfile: "Dockerfile"}})
	if err != nil || prep.StageName != "dev_container_auto_added_stage_label" {
		t.Fatalf("without a target: %+v, %v", prep, err)
	}
	if _, err := os.Stat(prep.Path); err != nil {
		t.Fatalf("named Dockerfile: %v", err)
	}
	prep.Cleanup()
	if _, err := os.Stat(prep.Path); !os.IsNotExist(err) {
		t.Errorf("named Dockerfile %s left after Cleanup: %v", prep.Path, err)
	}
	if _, err := prepareDockerfileBuild(&config.DevContainer{ConfigFilePath: cfgFile, Build: &config.Build{Dockerfile: "Empty"}}); err == nil || !strings.Contains(err.Error(), "no FROM instructions") {
		t.Errorf("no FROM: err = %v", err)
	}
}

// A Compose build without --image-name reports its image as a string, as TS
// doBuild does (imageNameResult = originalImageName); with --image-name, and
// for any other configuration, the names are an array.
func TestBuildResultImageName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compose  bool
		argNames []string
		built    []string
		want     any
	}{
		{"compose without --image-name", true, nil, []string{"proj-app"}, "proj-app"},
		{"compose with --image-name", true, []string{"a"}, []string{"a"}, []string{"a"}},
		{"image without --image-name", false, nil, []string{"vsc-x"}, []string{"vsc-x"}},
	} {
		if got := buildResultImageName(tc.compose, tc.argNames, tc.built); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: imageName = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}
