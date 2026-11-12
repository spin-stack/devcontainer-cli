package lifecycle

import (
	"errors"
	"slices"
	"testing"
)

func TestShellServerReportsNoExitCodeWhenTheExecFails(t *testing.T) {
	boom := errors.New("no such container")
	s := NewShellServer(t.Context(), &fakeExecutor{fn: func(string) (string, string, int, error) {
		return "out", "err", 7, boom
	}}, "cid", "")
	if out, code, err := s.Exec("true"); !errors.Is(err, boom) || out != "" || code != -1 {
		t.Errorf("Exec = (%q, %d, %v), want (\"\", -1, %v)", out, code, err, boom)
	}
	if out, errText, code, err := s.ExecWithStderr("true"); !errors.Is(err, boom) || out != "" || errText != "" || code != -1 {
		t.Errorf("ExecWithStderr = (%q, %q, %d, %v), want (\"\", \"\", -1, %v)", out, errText, code, err, boom)
	}
}

func TestShellExecutorLogsOnlyOutputThereIs(t *testing.T) {
	for _, tc := range []struct {
		stdout, stderr string
		want           []string
	}{
		{"", " \n", nil},
		{"out\n", "", []string{"out\n"}},
		{"", "warn\n", []string{"warn\n"}},
		{"out\n", "warn\n", []string{"out\n", "warn\n"}},
	} {
		logger := &captureLog{}
		e := &ShellExecutor{Server: NewShellServer(t.Context(), &fakeExecutor{fn: func(string) (string, string, int, error) {
			return tc.stdout, tc.stderr, 0, nil
		}}, "cid", ""), Log: logger}
		if err := e.Exec("hook"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(logger.lines, tc.want) {
			t.Errorf("stdout %q, stderr %q: logged %q, want %q", tc.stdout, tc.stderr, logger.lines, tc.want)
		}
	}
}
