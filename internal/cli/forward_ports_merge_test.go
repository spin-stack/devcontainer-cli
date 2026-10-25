package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/imagemeta"
)

// The config's forwardPorts reach the merged configuration as the raw
// number|string values, and de-dupe against the same ports from image metadata
// (TS mergeForwardPorts). They were passed as config.PortOrString structs, so
// the de-dupe formatted them with %v and emitted "{8080}".
func TestConfigForwardPortsMergeAsRawValues(t *testing.T) {
	var cfg config.DevContainer
	if err := json.Unmarshal([]byte(`{"image":"x","forwardPorts":[8080,"db:5432"]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	want := []any{float64(8080), "db:5432"}

	configOnly := imagemeta.MergeConfiguration([]imagemeta.Entry{configToMetadataEntry(&cfg)})
	if !reflect.DeepEqual(configOnly.ForwardPorts, want) {
		t.Errorf("config only: forwardPorts = %#v, want %#v", configOnly.ForwardPorts, want)
	}

	image := imagemeta.Entry{ForwardPorts: []any{float64(8080), "db:5432"}}
	withImage := imagemeta.MergeConfiguration([]imagemeta.Entry{image, configToMetadataEntry(&cfg)})
	if !reflect.DeepEqual(withImage.ForwardPorts, want) {
		t.Errorf("image + config: forwardPorts = %#v, want %#v (the same ports de-dupe)", withImage.ForwardPorts, want)
	}
}
