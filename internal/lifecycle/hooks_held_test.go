package lifecycle

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

func TestRunHooksErrorNamesTheFailedCommand(t *testing.T) {
	for want, command := range map[string]any{
		"postCreateCommand failed: command failed: bad":    "bad",
		"postCreateCommand failed: b: command failed: bad": map[string]any{"a": "good", "b": "bad"},
	} {
		exec := NewMockCommandExecutor(gomock.NewController(t))
		if _, ok := command.(map[string]any); ok {
			exec.EXPECT().Exec("good")
		}
		exec.EXPECT().Exec("bad").Return(errors.New("command failed: bad"))
		merged := &imagemeta.MergedConfig{PostCreateCommands: []any{command}}
		err := RunHooks(log.Null, exec, merged, RunOptions{})
		if err == nil || err.Error() != want {
			t.Errorf("RunHooks(%v) = %v, want %q", command, err, want)
		}
	}
}
