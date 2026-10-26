package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
)

// The configuration's label entry is TS pick(config.raw, pickConfigProperties):
// variables stay unresolved, properties the label does not record (runArgs,
// image, features) are left out, and --omit-config-remote-env-from-metadata drops
// remoteEnv.
func TestConfigLabelEntryPicksTheRawConfig(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{
		"image": "debian",
		"runArgs": ["-e", "X=1"],
		"features": {"ghcr.io/a/b/c:1": {}},
		"remoteUser": "vscode",
		"containerEnv": {"LOCAL_PATH": "${localEnv:PATH}"},
		"remoteEnv": {"TEST": "ENV"},
		"postCreateCommand": "echo hi",
		"forwardPorts": [8080, "db:5432"]
	}`), &raw); err != nil {
		t.Fatal(err)
	}
	entry, err := configLabelEntry(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(entry)
	want := `{"postCreateCommand":"echo hi","containerEnv":{"LOCAL_PATH":"${localEnv:PATH}"},"remoteUser":"vscode","remoteEnv":{"TEST":"ENV"},"forwardPorts":[8080,"db:5432"]}`
	if string(got) != want {
		t.Errorf("entry = %s\nwant    %s", got, want)
	}
	omitted, err := configLabelEntry(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if omitted.RemoteEnv != nil {
		t.Errorf("remoteEnv kept with omitRemoteEnv: %v", omitted.RemoteEnv)
	}
	if _, err := configLabelEntry(map[string]any{"init": "yes"}, false); err == nil {
		t.Error("an init that is no boolean: want an error")
	}
}

// A container gets "[]" without metadata (TS JSON.stringify of no entries); an
// image build gets no label at all, as TS's Dockerfile has no LABEL line.
func TestMetadataLabelsWithoutEntries(t *testing.T) {
	if got := containerMetadataLabel(nil); got != "[]" {
		t.Errorf("container label = %q, want []", got)
	}
	if got := containerMetadataLabel([]imagemeta.Entry{{RemoteUser: "a"}}); got != `[{"remoteUser":"a"}]` {
		t.Errorf("container label = %q", got)
	}
	if got := imageLabelArgs([]imagemeta.Entry{{}}); got != nil {
		t.Errorf("image labels = %q, want none", got)
	}
	if got := imageLabelArgs([]imagemeta.Entry{{RemoteUser: "a"}}); !reflect.DeepEqual(got, []string{`devcontainer.metadata=[{"remoteUser":"a"}]`}) {
		t.Errorf("image labels = %q", got)
	}
}

// TS getImageMetadataFromContainer: the container's label entries, substituted;
// for a container with every id label, which records the configuration's entry,
// only the configuration's updateable properties follow, so its hooks do not
// run twice; for any other the Features' entries and the configuration's; no
// label gives the configuration's entry alone.
func TestImageMetadataFromContainer(t *testing.T) {
	cfg := &config.DevContainer{RemoteUser: "me", UserEnvProbe: "none", RemoteEnv: map[string]*string{"A": strPtr("b")}}
	cfg.PostCreateCommand = config.LifecycleCommand{}
	if err := json.Unmarshal([]byte(`"cfg-pc"`), &cfg.PostCreateCommand); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{
		imagemeta.MetadataLabel:     `[{"id":"feat","postCreateCommand":"feat-pc"},{"postCreateCommand":"cfg-pc","containerEnv":{"P":"${localEnv:HOME}"}}]`,
		"devcontainer.local_folder": "/ws",
	}
	substitute := hostMetadataSubstitution(config.HostSubContext{Env: map[string]string{"HOME": "/home/me"}}, nil)
	features := []imagemeta.Entry{{ID: "f2"}}

	got, err := imageMetadataFromContainer(labels, cfg, features, []string{"devcontainer.local_folder=/ws"}, substitute, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	merged := imagemeta.MergeConfiguration(got)
	if !reflect.DeepEqual(merged.PostCreateCommands, []any{"feat-pc", "cfg-pc"}) {
		t.Errorf("with the id labels: postCreateCommands = %v, want the label's alone", merged.PostCreateCommands)
	}
	if merged.ContainerEnv["P"] != "/home/me" || merged.RemoteUser != "me" || merged.UserEnvProbe != "none" || *merged.RemoteEnv["A"] != "b" {
		t.Errorf("with the id labels: merged = %+v, want the label substituted and the updateable properties", merged)
	}

	got, err = imageMetadataFromContainer(labels, cfg, features, []string{"other=label"}, substitute, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[2].ID != "f2" || got[3].PostCreateCommand != "cfg-pc" {
		t.Errorf("without the id labels: %+v, want the label, the Features and the config's entry", got)
	}

	got, err = imageMetadataFromContainer(map[string]string{}, cfg, features, nil, substitute, quietLogger())
	if err != nil || len(got) != 2 || got[0].ID != "f2" || got[1].RemoteUser != "me" {
		t.Errorf("without a label: %+v, %v; want the Features and the config's entry", got, err)
	}

	if got, err := imageMetadataFromContainer(labels, nil, features, nil, substitute, quietLogger()); err != nil || len(got) != 2 {
		t.Errorf("without a config: %+v, %v; want the label's entries", got, err)
	}

	failing := func(*[]imagemeta.Entry) error { return errors.New("boom") }
	if _, err := imageMetadataFromContainer(labels, cfg, nil, nil, failing, quietLogger()); err == nil {
		t.Error("a failing substitution: want the error")
	}
}

// TS findContainerAndIdLabels: the --id-label values, else the workspace
// folder's and config file's labels, the config file's only for a container
// that has one.
func TestContainerIDLabels(t *testing.T) {
	if got := containerIDLabels([]string{"a=b"}, nil, "/ws", "/ws/c.json"); !reflect.DeepEqual(got, []string{"a=b"}) {
		t.Errorf("provided = %v", got)
	}
	withConfig := map[string]string{"devcontainer.config_file": "/ws/c.json"}
	if got := containerIDLabels(nil, withConfig, "/ws", "/ws/c.json"); !reflect.DeepEqual(got, []string{"devcontainer.local_folder=/ws", "devcontainer.config_file=/ws/c.json"}) {
		t.Errorf("container with a config file label = %v", got)
	}
	if got := containerIDLabels(nil, map[string]string{}, "/ws", "/ws/c.json"); !reflect.DeepEqual(got, []string{"devcontainer.local_folder=/ws"}) {
		t.Errorf("older container = %v", got)
	}
	// Without a workspace folder or a config file, TS's template literal
	// writes undefined (--container-id alone).
	if got := containerIDLabels(nil, withConfig, "", ""); !reflect.DeepEqual(got, []string{"devcontainer.local_folder=undefined", "devcontainer.config_file=undefined"}) {
		t.Errorf("no workspace folder or config file = %v", got)
	}
}

// The label's entries get the host substitutions, and ${devcontainerId} only
// with id labels to compute it from.
func TestHostMetadataSubstitution(t *testing.T) {
	hostSub := config.HostSubContext{Env: map[string]string{"U": "me"}}
	entries := []imagemeta.Entry{{RemoteUser: "${localEnv:U}", ContainerEnv: map[string]string{"ID": "${devcontainerId}"}}}
	if err := hostMetadataSubstitution(hostSub, nil)(&entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].RemoteUser != "me" || entries[0].ContainerEnv["ID"] != "${devcontainerId}" {
		t.Errorf("without id labels: %+v", entries[0])
	}
	idLabels := []string{"devcontainer.local_folder=/ws"}
	if err := hostMetadataSubstitution(hostSub, idLabels)(&entries); err != nil {
		t.Fatal(err)
	}
	if want := config.ComputeDevContainerID(config.EnvMap(idLabels)); entries[0].ContainerEnv["ID"] != want {
		t.Errorf("with id labels: ID = %q, want %q", entries[0].ContainerEnv["ID"], want)
	}
}
