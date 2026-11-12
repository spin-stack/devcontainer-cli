package cli

import (
	"encoding/json"
	"testing"
)

func TestGPURequested(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`null`, false},
		{`false`, false},
		{`true`, true},
		{`"optional"`, true},
		{`{"cores":1}`, true},
	}
	for _, tt := range tests {
		got := gpuRequested(json.RawMessage(tt.raw))
		if got != tt.want {
			t.Errorf("gpuRequested(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
