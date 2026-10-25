package log

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Without a Writer the logger writes to stderr, where the TS CLI writes its log.
func TestNewWritesToStderrByDefault(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved })

	New(Options{}).Write("to stderr", LevelInfo)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if !strings.HasSuffix(string(out), "] to stderr\n") {
		t.Errorf("stderr = %q, want the line", out)
	}
}

// Without a StartTime the elapsed time counts from New, so the first line
// reads a few milliseconds, not the milliseconds since 1970.
func TestNewCountsElapsedFromNowByDefault(t *testing.T) {
	var buf bytes.Buffer
	New(Options{Writer: &buf}).Write("x", LevelInfo)
	m := regexp.MustCompile(`^\[(-?\d+) ms\] x\n$`).FindStringSubmatch(buf.String())
	if m == nil {
		t.Fatalf("output = %q", buf.String())
	}
	if ms, _ := strconv.ParseInt(m[1], 10, 64); ms < 0 || ms > 60_000 {
		t.Errorf("elapsed = %d ms, want the time since New", ms)
	}
}

// An unset level is info, the TS CLI default: debug lines are hidden.
func TestNewDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Write("hidden", LevelDebug)
	l.Write("shown", LevelInfo)
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Errorf("output = %q, want only the info line", out)
	}
}

// The banner is TS createLog's header (spec-node/devContainers.ts:
// `${pkg.name} ${pkg.version}. Node.js ${process.version}. ${os.platform()}
// ${os.release()} ${os.arch()}.`), the Go runtime standing in for Node.
func TestBannerLineNamesTheKernelRelease(t *testing.T) {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Skipf("no kernel release to compare: %v", err)
	}
	want := fmt.Sprintf("@devcontainers/cli 1.2.3. Go %s. %s %s %s.", runtime.Version(), runtime.GOOS, strings.TrimSpace(string(b)), runtime.GOARCH)
	if got := BannerLine("1.2.3"); got != want {
		t.Errorf("BannerLine = %q, want %q", got, want)
	}
}

// A line's [N ms] is its timestamp minus the logger's start, and each text
// ends in exactly one newline, as TS logEventToTerminalText
// (spec-utils/log.ts: `${e.timestamp - startTimestamp} ms`, and
// toTerminalText's replace(/(\r?\n)?$/, ...) adding a line end only when
// there is none).
func TestTerminalLinesCountFromTheStartAndEndInOneNewline(t *testing.T) {
	start := time.UnixMilli(1_000)
	for _, tc := range []struct{ text, want string }{
		{"a", "[1234 ms] a\n"},
		{"a\n", "[1234 ms] a\n"},
		{"ab\n", "[1234 ms] ab\n"},
		{"\n", "[1234 ms] \n"},
		{"", "[1234 ms] \n"},
	} {
		var buf bytes.Buffer
		newTerminalHandler(&buf, start, LevelInfo).HandleEvent(Event{Type: "text", Level: LevelInfo, Timestamp: 2_234, Text: tc.text})
		if buf.String() != tc.want {
			t.Errorf("text %q printed %q, want %q", tc.text, buf.String(), tc.want)
		}
	}
}

// A stop line reads the elapsed time since the start and, in parentheses, the
// time since its own start event (spec-utils/log.ts: `Stop (${e.timestamp -
// e.startTimestamp} ms)`).
func TestTerminalStopLineShowsItsDuration(t *testing.T) {
	var buf bytes.Buffer
	newTerminalHandler(&buf, time.UnixMilli(1_000), LevelTrace).HandleEvent(Event{Type: "stop", Level: LevelInfo, Timestamp: 2_234, StartTimestamp: 2_000, Text: "Run: x"})
	if want := "[1234 ms] Stop (234 ms): Run: x\n"; buf.String() != want {
		t.Errorf("printed %q, want %q", buf.String(), want)
	}
}
