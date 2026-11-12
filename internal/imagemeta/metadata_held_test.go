package imagemeta

import (
	"bytes"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestReadMetadataFromLabelsTakesAnEmptyLabelAsNone(t *testing.T) {
	for _, tt := range []struct {
		name, label string
		warns       bool
	}{
		{"empty", "", false},
		{"unparsable", "x", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.New(log.Options{Level: log.LevelTrace, Writer: &buf})
			if got := ReadMetadataFromLabels(map[string]string{MetadataLabel: tt.label}, logger); got != nil {
				t.Errorf("entries = %#v, want nil", got)
			}
			if warned := bytes.Contains(buf.Bytes(), []byte("Failed to parse image metadata label")); warned != tt.warns {
				t.Errorf("warned = %v, want %v; log: %q", warned, tt.warns, buf.String())
			}
		})
	}
}
