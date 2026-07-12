// Package errors defines the CLI's structured error types and JSON error envelope.
package errors

import (
	"errors"
	"fmt"
)

// ContainerError is the primary error type for the devcontainer CLI.
// It carries structured metadata that is serialized into the JSON output envelope.
type ContainerError struct {
	Description         string
	OriginalError       error
	ContainerID         string
	DisallowedFeatureID string
	DidStopContainer    bool
	LearnMoreURL        string
}

func (e *ContainerError) Error() string {
	if e.ContainerID != "" {
		return fmt.Sprintf("%s (container: %s)", e.Description, e.ContainerID)
	}
	return e.Description
}

func (e *ContainerError) Unwrap() error {
	return e.OriginalError
}

// ExitCodeError wraps an error with a specific exit code for the process.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit code %d", e.Code)
}

func (e *ExitCodeError) Unwrap() error { return e.Err }

// ErrorOutput is the JSON envelope for error responses.
// Fields match the TS CLI exactly for parity.
type ErrorOutput struct {
	Outcome             string `json:"outcome"`
	Message             string `json:"message"`
	Description         string `json:"description"`
	ContainerID         string `json:"containerId,omitempty"`
	DisallowedFeatureID string `json:"disallowedFeatureId,omitempty"`
	DidStopContainer    *bool  `json:"didStopContainer,omitempty"`
	LearnMoreURL        string `json:"learnMoreUrl,omitempty"`
}

// ToErrorOutput converts any error into the standard CLI error JSON envelope.
func ToErrorOutput(err error) ErrorOutput {
	ce, ok := AsContainerError(err)
	if !ok {
		return ErrorOutput{
			Outcome:     "error",
			Message:     err.Error(),
			Description: err.Error(),
		}
	}

	out := ErrorOutput{
		Outcome:             "error",
		Message:             ce.Error(),
		Description:         ce.Description,
		ContainerID:         ce.ContainerID,
		DisallowedFeatureID: ce.DisallowedFeatureID,
		LearnMoreURL:        ce.LearnMoreURL,
	}
	if ce.DidStopContainer {
		v := true
		out.DidStopContainer = &v
	}
	return out
}

// AsContainerError extracts a *ContainerError from an error chain. It is a thin
// wrapper over errors.As, so it also traverses errors that implement
// As(any) bool or the multi-error Unwrap() []error (e.g. errors.Join).
func AsContainerError(err error) (*ContainerError, bool) {
	var ce *ContainerError
	ok := errors.As(err, &ce)
	return ce, ok
}
