package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Level mirrors the TS enum values for compatibility.
type Level int

const (
	LevelTrace    Level = 1
	LevelDebug    Level = 2
	LevelInfo     Level = 3
	LevelWarning  Level = 4
	LevelError    Level = 5
	LevelCritical Level = 6
	LevelOff      Level = 7
)

func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarning:
		return "warning"
	case LevelError:
		return "error"
	case LevelCritical:
		return "critical"
	case LevelOff:
		return "off"
	default:
		return "unknown"
	}
}

// ParseLevel converts a CLI string flag to a Level.
func ParseLevel(text string) Level {
	switch strings.ToLower(text) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Dimensions holds terminal size for formatting.
type Dimensions struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// Event represents a structured log event, matching the TS LogEvent union.
type Event struct {
	Type           string `json:"type"`                     // "text", "raw", "start", "stop", "progress"
	Level          Level  `json:"level,omitempty"`          // for text/raw/start/stop
	Timestamp      int64  `json:"timestamp,omitempty"`      // unix millis
	Text           string `json:"text,omitempty"`           // for text/raw/start/stop
	StartTimestamp int64  `json:"startTimestamp,omitempty"` // for stop
	Name           string `json:"name,omitempty"`           // for progress
	Status         string `json:"status,omitempty"`         // for progress: "running","succeeded","failed"
	StepDetail     string `json:"stepDetail,omitempty"`     // for progress
	Channel        string `json:"channel,omitempty"`        // optional grouping
}

// Logger is the primary logging interface consumed by all subsystems.
type Logger interface {
	Write(text string, level ...Level)
	Raw(text string, level ...Level)
	Start(text string, level ...Level) int64
	Stop(text string, startTimestamp int64, level ...Level)
	Event(e Event)
	Dimensions() *Dimensions
}

// Handler processes log events. Multiple handlers can be combined.
type Handler interface {
	HandleEvent(e Event)
}

// Options configures a new Logger instance.
type Options struct {
	Level      Level
	Format     string // "text" or "json"
	Writer     io.Writer
	Dimensions *Dimensions
	Version    string
	StartTime  time.Time
	// Secrets, when set, are replaced with "********" in every emitted log line
	// (matching the TS CLI maskSecrets). Pass the secret *values*.
	Secrets []string
}

type logger struct {
	handler      Handler
	defaultLevel Level
	dimensions   *Dimensions
	secrets      []string
}

// New creates a Logger from Options. It selects the appropriate handler
// based on Format.
func New(opts Options) Logger {
	if opts.Writer == nil {
		opts.Writer = os.Stderr
	}
	if opts.StartTime.IsZero() {
		opts.StartTime = time.Now()
	}

	// Default to info when unset, matching the TS CLI default log level.
	if opts.Level == 0 {
		opts.Level = LevelInfo
	}

	var h Handler
	switch opts.Format {
	case "json":
		// The JSON stream emits every event regardless of --log-level; the
		// consumer (e.g. the VS Code extension) filters client-side. Only the
		// terminal handler applies the level filter, matching the TS CLI.
		h = newJSONHandler(opts.Writer)
	default:
		h = newTerminalHandler(opts.Writer, opts.StartTime, opts.Level)
	}

	l := &logger{
		handler:      h,
		defaultLevel: opts.Level,
		dimensions:   opts.Dimensions,
		secrets:      prepareSecrets(opts.Secrets),
	}

	// Emit the version banner as the first text event, like the TS CLI header
	// (createLog with omitHeader=false). Only top-level command loggers set
	// Version; sub-loggers leave it empty to avoid a duplicate banner.
	if opts.Version != "" {
		l.Event(Event{
			Type:      "text",
			Level:     LevelInfo,
			Timestamp: nowMillis(),
			Text:      BannerLine(opts.Version),
		})
	}

	return l
}

// BannerLine builds the startup banner, mirroring the TS CLI format
// "@devcontainers/cli <ver>. Node.js <ver>. <platform> <release> <arch>." with
// the Go runtime substituted for Node.
func BannerLine(version string) string {
	platform := runtime.GOOS
	if rel := osRelease(); rel != "" {
		platform += " " + rel
	}
	return fmt.Sprintf("@devcontainers/cli %s. Go %s. %s %s.", version, runtime.Version(), platform, runtime.GOARCH)
}

// osRelease returns the kernel release string (best-effort; empty on failure).
func osRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// prepareSecrets drops empty values and sorts by length descending so that
// longer secrets are masked before any shorter substring of them.
func prepareSecrets(values []string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func (l *logger) resolveLevel(level []Level) Level {
	if len(level) > 0 {
		return level[0]
	}
	return LevelDebug
}

func (l *logger) Write(text string, level ...Level) {
	l.Event(Event{
		Type:      "text",
		Level:     l.resolveLevel(level),
		Timestamp: nowMillis(),
		Text:      text,
	})
}

func (l *logger) Raw(text string, level ...Level) {
	l.Event(Event{
		Type:      "raw",
		Level:     l.resolveLevel(level),
		Timestamp: nowMillis(),
		Text:      text,
	})
}

func (l *logger) Start(text string, level ...Level) int64 {
	ts := nowMillis()
	l.Event(Event{
		Type:      "start",
		Level:     l.resolveLevel(level),
		Timestamp: ts,
		Text:      text,
	})
	return ts
}

func (l *logger) Stop(text string, startTimestamp int64, level ...Level) {
	l.Event(Event{
		Type:           "stop",
		Level:          l.resolveLevel(level),
		Timestamp:      nowMillis(),
		Text:           text,
		StartTimestamp: startTimestamp,
	})
}

func (l *logger) Event(e Event) {
	if len(l.secrets) > 0 && e.Text != "" {
		for _, s := range l.secrets {
			e.Text = strings.ReplaceAll(e.Text, s, "********")
		}
	}
	l.handler.HandleEvent(e)
}

func (l *logger) Dimensions() *Dimensions {
	return l.dimensions
}

// Null is a no-op logger.
var Null Logger = &nullLog{}

type nullLog struct{}

func (n *nullLog) Write(string, ...Level)       {}
func (n *nullLog) Raw(string, ...Level)         {}
func (n *nullLog) Start(string, ...Level) int64 { return nowMillis() }
func (n *nullLog) Stop(string, int64, ...Level) {}
func (n *nullLog) Event(Event)                  {}
func (n *nullLog) Dimensions() *Dimensions      { return nil }

// --- Terminal handler ---

type terminalHandler struct {
	w        io.Writer
	start    int64
	minLevel Level
}

func newTerminalHandler(w io.Writer, start time.Time, minLevel Level) Handler {
	if minLevel == 0 {
		minLevel = LevelInfo
	}
	return &terminalHandler{w: w, start: start.UnixMilli(), minLevel: minLevel}
}

func (h *terminalHandler) HandleEvent(e Event) {
	if e.Type == "progress" {
		return // progress events are not printed in terminal mode
	}
	// Filter by --log-level: drop events below the configured threshold, like
	// the TS CLI terminal handler (e.g. at info, debug "Run:" lines are hidden).
	if e.Level == 0 || e.Level < h.minLevel {
		return
	}
	elapsed := e.Timestamp - h.start
	var line string
	switch e.Type {
	case "text":
		line = fmt.Sprintf("[%d ms] %s", elapsed, ensureNewline(e.Text))
	case "raw":
		line = e.Text
	case "start":
		line = fmt.Sprintf("[%d ms] Start: %s", elapsed, ensureNewline(e.Text))
	case "stop":
		duration := e.Timestamp - e.StartTimestamp
		line = fmt.Sprintf("[%d ms] Stop (%d ms): %s", elapsed, duration, ensureNewline(e.Text))
	}
	if line != "" {
		fmt.Fprint(h.w, line)
	}
}

// --- JSON handler ---

type jsonHandler struct {
	w io.Writer
}

func newJSONHandler(w io.Writer) Handler {
	return &jsonHandler{w: w}
}

func (h *jsonHandler) HandleEvent(e Event) {
	data, _ := json.Marshal(e)
	fmt.Fprintf(h.w, "%s\n", data)
}

// --- Helpers ---

func nowMillis() int64 {
	return time.Now().UnixMilli()
}

func ensureNewline(s string) string {
	if len(s) == 0 || s[len(s)-1] != '\n' {
		return s + "\n"
	}
	return s
}
