package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// fakeEngineAPI is a Docker engine with local images, images a pull makes
// local, and containers. The docker.API methods it does not override are
// not expected to be called.
type fakeEngineAPI struct {
	docker.API
	images     map[string]string // name -> image inspect JSON
	pullable   map[string]string
	pulls      []string
	containers map[string]string // id -> container inspect JSON
	listed     [][]string        // label filters of each ContainerList
	list       func(labels []string) []string
}

func (f *fakeEngineAPI) ImageInspect(_ context.Context, name string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
	var res mobyclient.ImageInspectResult
	js, ok := f.images[name]
	if !ok {
		return res, errors.New("No such image: " + name)
	}
	return res, json.Unmarshal([]byte(js), &res.InspectResponse)
}

func (f *fakeEngineAPI) ImagePull(_ context.Context, name string, _ mobyclient.ImagePullOptions) (mobyclient.ImagePullResponse, error) {
	f.pulls = append(f.pulls, name)
	js, ok := f.pullable[name]
	if !ok {
		return nil, errors.New("pull access denied for " + name)
	}
	if f.images == nil {
		f.images = map[string]string{}
	}
	f.images[name] = js
	return emptyPull{}, nil
}

func (f *fakeEngineAPI) ContainerInspect(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	var res mobyclient.ContainerInspectResult
	js, ok := f.containers[id]
	if !ok {
		return res, errors.New("No such container: " + id)
	}
	return res, json.Unmarshal([]byte(js), &res.Container)
}

func (f *fakeEngineAPI) ContainerList(_ context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	var labels []string
	for l := range opts.Filters["label"] {
		labels = append(labels, l)
	}
	f.listed = append(f.listed, labels)
	var res mobyclient.ContainerListResult
	if f.list != nil {
		for _, id := range f.list(labels) {
			res.Items = append(res.Items, container.Summary{ID: id})
		}
	}
	return res, nil
}

func (f *fakeEngineAPI) Close() error { return nil }

// emptyPull is a pull whose progress stream is empty.
type emptyPull struct{ mobyclient.ImagePullResponse }

func (emptyPull) Read([]byte) (int, error) { return 0, io.EOF }
func (emptyPull) Close() error             { return nil }

func TestNewImageInspectorWithoutEngine(t *testing.T) {
	insp := newImageInspector(context.Background(), log.Null, nil)
	insp.pullInterval = 0
	// An invalid name fails the registry inspect without contacting a registry.
	got, err := insp.inspect(context.Background(), "Not A Valid Name")
	if !errors.Is(err, errDockerEngineUnavailable) || got != nil {
		t.Fatalf("inspect = %+v, %v; want the pull to fail with %v", got, err, errDockerEngineUnavailable)
	}
	if _, err := insp.local(context.Background(), "img"); !errors.Is(err, errDockerEngineUnavailable) {
		t.Fatalf("local inspect without an engine = %v, want %v", err, errDockerEngineUnavailable)
	}
}

func TestNewImageInspectorLocalImage(t *testing.T) {
	api := &fakeEngineAPI{images: map[string]string{
		"img:1": `{"Config":{"User":"node","Env":["A=b"],"Labels":{"l":"v"}}}`,
		"bare":  `{}`,
	}}
	insp := newImageInspector(context.Background(), log.Null, &docker.EngineClient{API: api, Log: log.Null})
	got, err := insp.inspect(context.Background(), "img:1")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got.User != "node" || len(got.Env) != 1 || got.Env[0] != "A=b" || got.Labels["l"] != "v" {
		t.Fatalf("inspect = %+v, want the local image's user, env and labels", got)
	}
	if got, err = insp.inspect(context.Background(), "bare"); err != nil || got.User != "" || got.Labels != nil {
		t.Fatalf("image without config: inspect = %+v, %v; want an empty config", got, err)
	}
	if len(api.pulls) != 0 {
		t.Errorf("pulled %v; a local image is not pulled", api.pulls)
	}
}

func TestNewImageInspectorPullsWhatTheRegistryCannotInspect(t *testing.T) {
	const name = "Not A Valid Name"
	api := &fakeEngineAPI{pullable: map[string]string{name: `{"Config":{"User":"pulled"}}`}}
	insp := newImageInspector(context.Background(), log.Null, &docker.EngineClient{API: api, Log: log.Null})
	got, err := insp.inspect(context.Background(), name)
	if err != nil || got.User != "pulled" {
		t.Fatalf("inspect = %+v, %v; want the pulled image's config", got, err)
	}
	if len(api.pulls) != 1 {
		t.Errorf("pulls = %v, want one", api.pulls)
	}
}

func TestImageBuildInfoComposeBuildWithoutContext(t *testing.T) {
	// A build without a context builds from the first compose file's folder.
	dir := writeConfigFixture(t, map[string]string{"compose/compose.yml": "services: {}\n", "compose/Dockerfile": "FROM ctx-base:1\n"})
	cfg := &config.DevContainer{ConfigFilePath: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: []string{"compose/compose.yml"}, Service: "app"}
	f := &fakeImages{registry: map[string]*oci.ImageConfig{"ctx-base:1": metaImage("dev", "from-context")}}
	compose := func(context.Context, []string, string) (map[string]any, error) {
		return map[string]any{"services": map[string]any{"app": map[string]any{"build": map[string]any{}}}}, nil
	}
	info, err := getImageBuildInfo(context.Background(), cfg, f.inspector(), compose, log.Null)
	if err != nil || info.User != "dev" || len(info.Metadata) != 1 || info.Metadata[0].RemoteUser != "from-context" {
		t.Fatalf("info = %+v, %v; want the metadata of the Dockerfile next to the compose file", info, err)
	}
}

func TestImageBuildInfoDockerfileUserFromBaseImageEnv(t *testing.T) {
	f := &fakeImages{local: map[string]*oci.ImageConfig{"base": {User: "root", Env: []string{"IMG_USER=vscode"}}}}
	info, err := imageBuildInfoFromDockerfile(context.Background(), f.inspector(), "FROM base\nUSER ${IMG_USER}\n", nil, "", log.Null)
	if err != nil || info.User != "vscode" {
		t.Fatalf("info = %+v, %v; want the USER resolved with the base image's env", info, err)
	}
}
