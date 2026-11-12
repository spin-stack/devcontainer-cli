package errors

import (
	"errors"
	"fmt"
)

type ContainerError struct {
	Description         string
	OriginalError       error
	ContainerID         string
	DisallowedFeatureID string
	DidStopContainer    *bool
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

type ReferenceError interface {
	error
	ReferenceError()
}

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

func ToErrorOutput(err error) ErrorOutput {
	ce, ok := AsContainerError(err)
	if !ok {
		return ErrorOutput{
			Outcome:     "error",
			Message:     err.Error(),
			Description: err.Error(),
		}
	}

	message := ce.Description
	if ce.OriginalError != nil {
		message = ce.OriginalError.Error()
	}
	out := ErrorOutput{
		Outcome:             "error",
		Message:             message,
		Description:         ce.Description,
		ContainerID:         ce.ContainerID,
		DisallowedFeatureID: ce.DisallowedFeatureID,
		DidStopContainer:    ce.DidStopContainer,
		LearnMoreURL:        ce.LearnMoreURL,
	}
	return out
}

func AsContainerError(err error) (*ContainerError, bool) {
	var ce *ContainerError
	ok := errors.As(err, &ce)
	return ce, ok
}
