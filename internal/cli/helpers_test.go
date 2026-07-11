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
		{``, false},           // absent
		{`null`, false},       // null
		{`false`, false},      // explicit false must NOT enable GPUs
		{`true`, true},        // required
		{`"optional"`, true},  // optional → requested (availability decides)
		{`{"cores":1}`, true}, // object form
	}
	for _, tt := range tests {
		got := gpuRequested(json.RawMessage(tt.raw))
		if got != tt.want {
			t.Errorf("gpuRequested(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
