package errors

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestContainerError_Error(t *testing.T) {
	e := &ContainerError{Description: "An error occurred"}
	if e.Error() != "An error occurred" {
		t.Errorf("got %q", e.Error())
	}

	e2 := &ContainerError{Description: "fail", ContainerID: "abc123"}
	if e2.Error() != "fail (container: abc123)" {
		t.Errorf("got %q", e2.Error())
	}
}

func TestContainerError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("root cause")
	e := &ContainerError{Description: "wrapper", OriginalError: inner}
	if e.Unwrap() != inner {
		t.Error("Unwrap did not return original error")
	}
}

func TestToErrorOutput(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		check func(t *testing.T, out ErrorOutput)
	}{
		{
			name: "ContainerError",
			err: &ContainerError{
				Description:         "An error occurred setting up the container.",
				ContainerID:         "abc123",
				DisallowedFeatureID: "ghcr.io/bad/feature",
				DidStopContainer:    true,
				LearnMoreURL:        "https://example.com",
			},
			check: func(t *testing.T, out ErrorOutput) {
				if out.Outcome != "error" {
					t.Errorf("outcome = %q", out.Outcome)
				}
				if out.Description != "An error occurred setting up the container." {
					t.Errorf("description = %q", out.Description)
				}
				if out.ContainerID != "abc123" {
					t.Errorf("containerId = %q", out.ContainerID)
				}
				if out.DisallowedFeatureID != "ghcr.io/bad/feature" {
					t.Errorf("disallowedFeatureId = %q", out.DisallowedFeatureID)
				}
				if out.DidStopContainer == nil || !*out.DidStopContainer {
					t.Error("didStopContainer should be true")
				}
				if out.LearnMoreURL != "https://example.com" {
					t.Errorf("learnMoreUrl = %q", out.LearnMoreURL)
				}

				// Verify JSON serialization matches TS format
				data, _ := json.Marshal(out)
				var m map[string]any
				json.Unmarshal(data, &m)
				if m["outcome"] != "error" {
					t.Error("JSON outcome mismatch")
				}
			},
		},
		{
			name: "GenericError",
			err:  fmt.Errorf("something went wrong"),
			check: func(t *testing.T, out ErrorOutput) {
				if out.Outcome != "error" {
					t.Errorf("outcome = %q", out.Outcome)
				}
				if out.Description != "something went wrong" {
					t.Errorf("description = %q", out.Description)
				}
				if out.ContainerID != "" {
					t.Errorf("containerId should be empty, got %q", out.ContainerID)
				}
			},
		},
		{
			name: "OmitsEmptyFields",
			err:  &ContainerError{Description: "simple error"},
			check: func(t *testing.T, out ErrorOutput) {
				data, _ := json.Marshal(out)
				var m map[string]any
				json.Unmarshal(data, &m)

				// These optional fields should not appear in JSON
				if _, ok := m["containerId"]; ok {
					t.Error("containerId should be omitted when empty")
				}
				if _, ok := m["disallowedFeatureId"]; ok {
					t.Error("disallowedFeatureId should be omitted when empty")
				}
				if _, ok := m["didStopContainer"]; ok {
					t.Error("didStopContainer should be omitted when false")
				}
				if _, ok := m["learnMoreUrl"]; ok {
					t.Error("learnMoreUrl should be omitted when empty")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, ToErrorOutput(tt.err))
		})
	}
}

func TestAsContainerError(t *testing.T) {
	inner := &ContainerError{Description: "inner"}
	wrapped := fmt.Errorf("wrap: %w", inner)

	ce, ok := AsContainerError(wrapped)
	if !ok || ce != inner {
		t.Error("should extract wrapped ContainerError")
	}

	_, ok = AsContainerError(fmt.Errorf("plain"))
	if ok {
		t.Error("should not find ContainerError in plain error")
	}
}
