package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMapLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  LogLevel
	}{
		{"trace", LevelTrace},
		{"debug", LevelDebug},
		{"info", LevelInfo},
		{"error", LevelError},
		{"Trace", LevelTrace},
		{"INFO", LevelInfo},
		{"unknown", LevelInfo},
		{"", LevelInfo},
	}
	for _, tt := range tests {
		if got := MapLogLevel(tt.input); got != tt.want {
			t.Errorf("MapLogLevel(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestLogLevelString(t *testing.T) {
	if LevelInfo.String() != "info" {
		t.Errorf("LevelInfo.String() = %q", LevelInfo.String())
	}
	if LevelOff.String() != "off" {
		t.Errorf("LevelOff.String() = %q", LevelOff.String())
	}
}

func TestSecretMasking(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{
		Level:   LevelInfo,
		Format:  "text",
		Writer:  &buf,
		Secrets: []string{"s3cr3t", "", "tok"},
	})
	l.Write("connecting with token s3cr3t and tok", LevelInfo)
	out := buf.String()
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("secret leaked in output: %q", out)
	}
	if !strings.Contains(out, "********") {
		t.Errorf("expected mask in output: %q", out)
	}
	// The shorter secret is masked too.
	if strings.Contains(out, " tok ") {
		t.Errorf("short secret leaked: %q", out)
	}
}

func TestTerminalHandler_Text(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	l := New(Options{
		Level:     LevelInfo,
		Format:    "text",
		Writer:    &buf,
		StartTime: start,
	})

	l.Write("Starting container", LevelInfo)
	out := buf.String()

	if !strings.Contains(out, "ms]") {
		t.Errorf("expected timestamp, got: %q", out)
	}
	if !strings.Contains(out, "Starting container") {
		t.Errorf("expected message, got: %q", out)
	}
}

func TestTerminalHandler_StartStop(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	l := New(Options{
		Level:     LevelTrace,
		Format:    "text",
		Writer:    &buf,
		StartTime: start,
	})

	ts := l.Start("Build image")
	l.Stop("Build image", ts)
	out := buf.String()

	if !strings.Contains(out, "Start:") {
		t.Errorf("expected Start:, got: %q", out)
	}
	if !strings.Contains(out, "Stop") {
		t.Errorf("expected Stop, got: %q", out)
	}
}

func TestJSONHandler_Text(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{
		Level:  LevelInfo,
		Format: "json",
		Writer: &buf,
	})

	l.Write("hello world", LevelInfo)
	out := buf.String()

	var evt Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Type != "text" {
		t.Errorf("type = %q, want text", evt.Type)
	}
	if evt.Text != "hello world" {
		t.Errorf("text = %q, want 'hello world'", evt.Text)
	}
	if evt.Level != LevelInfo {
		t.Errorf("level = %d, want %d", evt.Level, LevelInfo)
	}
	if evt.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}
}

func TestJSONHandler_Progress(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{
		Level:  LevelInfo,
		Format: "json",
		Writer: &buf,
	})

	l.Event(Event{
		Type:   "progress",
		Name:   "Installing Features",
		Status: "running",
	})
	out := buf.String()

	var evt Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Type != "progress" {
		t.Errorf("type = %q, want progress", evt.Type)
	}
	if evt.Name != "Installing Features" {
		t.Errorf("name = %q", evt.Name)
	}
	if evt.Status != "running" {
		t.Errorf("status = %q", evt.Status)
	}
}

func TestNullLog(t *testing.T) {
	// Should not panic
	Null.Write("test")
	Null.Raw("test")
	ts := Null.Start("test")
	Null.Stop("test", ts)
	Null.Event(Event{Type: "text"})
	if Null.GetDimensions() != nil {
		t.Error("Null dimensions should be nil")
	}
}

func TestLogLevelFilter_TerminalVsJSON(t *testing.T) {
	// Terminal handler filters by --log-level (like the TS CLI): at info,
	// debug/trace lines are dropped; at debug, debug shows; trace shows all.
	cases := []struct {
		min                       LogLevel
		wantInfo, wantDebug, wantTrace bool
	}{
		{LevelInfo, true, false, false},
		{LevelDebug, true, true, false},
		{LevelTrace, true, true, true},
	}
	for _, c := range cases {
		var b bytes.Buffer
		l := New(Options{Level: c.min, Format: "text", Writer: &b})
		l.Write("INFO_LINE", LevelInfo)
		l.Write("DEBUG_LINE", LevelDebug)
		l.Write("TRACE_LINE", LevelTrace)
		s := b.String()
		if got := strings.Contains(s, "INFO_LINE"); got != c.wantInfo {
			t.Errorf("min=%s info=%v want %v", c.min, got, c.wantInfo)
		}
		if got := strings.Contains(s, "DEBUG_LINE"); got != c.wantDebug {
			t.Errorf("min=%s debug=%v want %v", c.min, got, c.wantDebug)
		}
		if got := strings.Contains(s, "TRACE_LINE"); got != c.wantTrace {
			t.Errorf("min=%s trace=%v want %v", c.min, got, c.wantTrace)
		}
	}

	// JSON handler emits everything regardless of level (consumer filters).
	var jb bytes.Buffer
	jl := New(Options{Level: LevelInfo, Format: "json", Writer: &jb})
	jl.Write("INFO_LINE", LevelInfo)
	jl.Write("DEBUG_LINE", LevelDebug)
	js := jb.String()
	if !strings.Contains(js, "INFO_LINE") || !strings.Contains(js, "DEBUG_LINE") {
		t.Errorf("json must emit all levels, got %q", js)
	}
}
