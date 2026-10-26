// Package log provides the CLI's structured, TS-compatible logger.
package log

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"
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
}

// Handler processes log events. Multiple handlers can be combined.
type Handler interface {
	HandleEvent(e Event)
}

// Options configures a new Logger instance.
type Options struct {
	Level     Level
	Format    string // "text" or "json"
	Writer    io.Writer
	Version   string
	StartTime time.Time
	// Secrets, when set, are replaced with "********" in every emitted log line
	// (matching the TS CLI maskSecrets). Pass the secret *values*.
	Secrets []string
}

type logger struct {
	handler      Handler
	defaultLevel Level
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

	// The handler is TS createLogFrom's (spec-node/devContainers.ts): JSON for
	// --log-format json, the terminal format when stdout is a terminal
	// (process.stdout.isTTY), the plain format otherwise.
	var h Handler
	switch {
	case opts.Format == "json":
		// The JSON stream emits every event regardless of --log-level; the
		// consumer (e.g. the VS Code extension) filters client-side. Only the
		// text handlers apply the level filter, matching the TS CLI.
		h = newJSONHandler(opts.Writer)
	case stdoutIsTerminal():
		h = newTerminalHandler(opts.Writer, opts.StartTime, opts.Level)
	default:
		h = newPlainHandler(opts.Writer, opts.Level)
	}

	l := &logger{
		handler:      h,
		defaultLevel: opts.Level,
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
	out := slices.DeleteFunc(slices.Clone(values), func(v string) bool { return v == "" })
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
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
	for _, s := range l.secrets {
		e.Text = strings.ReplaceAll(e.Text, s, "********")
	}
	l.handler.HandleEvent(e)
}

// Null is a no-op logger.
var Null Logger = &nullLog{}

type nullLog struct{}

func (n *nullLog) Write(string, ...Level)       {}
func (n *nullLog) Raw(string, ...Level)         {}
func (n *nullLog) Start(string, ...Level) int64 { return nowMillis() }
func (n *nullLog) Stop(string, int64, ...Level) {}
func (n *nullLog) Event(Event)                  {}

// --- Terminal handler ---

// stdoutIsTerminal reports what TS reads as process.stdout.isTTY: whether the
// CLI's stdout is a terminal. Like TS, the choice of log format follows stdout
// even though the log itself goes to stderr.
func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// terminalHandler is TS createTerminalLog: logEventToTerminalText
// (spec-utils/log.ts) with ANSI colors and \r\n line ends.
type terminalHandler struct {
	w        io.Writer
	start    int64
	minLevel Level
}

// newTerminalHandler's minLevel is never 0: New defaults it to info.
func newTerminalHandler(w io.Writer, start time.Time, minLevel Level) Handler {
	return &terminalHandler{w: w, start: start.UnixMilli(), minLevel: minLevel}
}

func (h *terminalHandler) HandleEvent(e Event) {
	// A progress event has no level (0, below any minLevel), so it is never
	// printed, as in TS.
	if e.Level < h.minLevel {
		return
	}
	elapsed := fmt.Sprintf("%d ms", e.Timestamp-h.start)
	trace := h.minLevel <= LevelTrace
	var line string
	switch e.Type {
	case "text":
		line = "[" + color(timestampColor, elapsed) + "] " + toTerminalText(e.Text)
	case "raw":
		line = e.Text
	case "start":
		if trace {
			line = color(startColor, "["+elapsed+"] Start") + ": " + toTerminalText(e.Text)
		} else {
			line = "[" + color(timestampColor, elapsed) + "] Start: " + toTerminalText(e.Text)
		}
	case "stop":
		// TS shows a stop line on the terminal only at trace.
		if trace {
			line = color(stopColor, "["+elapsed+"] Stop") + fmt.Sprintf(" (%d ms): ", e.Timestamp-e.StartTimestamp) + toTerminalText(e.Text)
		}
	}
	if line != "" {
		fmt.Fprint(h.w, line)
	}
}

// TS foreground colors (spec-utils/log.ts).
const (
	stopColor      = "38;2;143;99;79"
	startColor     = "38;2;99;143;79"
	timestampColor = "38;2;99;143;79"
	numberColor    = "38;2;86;156;214"
)

// color is TS color: s in bold and the given foreground color. TS colors each
// line of s; every caller passes one line, a timestamp or a number.
func color(c, s string) string {
	return "\x1b[1m\x1b[" + c + "m" + s + "\x1b[39m\x1b[22m"
}

// terminalEscapeSequences is TS terminalEscapeSequences (spec-utils/log.ts).
var terminalEscapeSequences = regexp.MustCompile(`(\x9B|\x1B\[)[0-?]*[ -/]*[@-~]`)

// newlines matches TS toTerminalText's /\r?\n/g.
var newlines = regexp.MustCompile(`\r?\n`)

// toTerminalText is TS toTerminalText: the text colorized, every line ending
// in \r\n, and one \r\n at the end.
func toTerminalText(text string) string {
	s := newlines.ReplaceAllString(colorize(text), "\r\n")
	if !strings.HasSuffix(s, "\r\n") {
		s += "\r\n"
	}
	return s
}

// colorize is TS colorize: numbers colored outside escape sequences, which are
// kept as they are.
func colorize(text string) string {
	var b strings.Builder
	last := 0
	for _, m := range terminalEscapeSequences.FindAllStringIndex(text, -1) { // mutate-exempt: any negative n finds every match
		b.WriteString(colorizePlainText(text[last:m[0]]))
		b.WriteString(text[m[0]:m[1]])
		last = m[1]
	}
	b.WriteString(colorizePlainText(text[last:]))
	return b.String()
}

// colorizePlainText is TS colorizePlainText, whose expression
// /(?<=^|[^A-Za-z0-9_\-\.])[0-9]+(\.[0-9]+)*(?=$|[^A-Za-z0-9_\-\.])/g needs
// lookaround that Go's regexp lacks: a number such as 1 or 1.2.3 is colored
// when neither neighbour is a letter, digit, '_', '-' or '.'.
func colorizePlainText(text string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(text); i++ {
		if !isDigit(text[i]) || (i > 0 && isWordByte(text[i-1])) {
			continue
		}
		j := skipDigits(text, i)
		for j+1 < len(text) && text[j] == '.' && isDigit(text[j+1]) {
			j = skipDigits(text, j+1) // mutate-exempt: text[j+1] is a digit, so skipping from j+2 ends at the same place
		}
		// No shorter match can pass the lookahead: it would end before a
		// digit or a '.', so a failed number is skipped whole.
		if j == len(text) || !isWordByte(text[j]) {
			b.WriteString(text[last:i])
			b.WriteString(color(numberColor, text[i:j]))
			last = j
		}
		i = j - 1
	}
	b.WriteString(text[last:])
	return b.String()
}

func skipDigits(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// isWordByte is the complement of TS's [^A-Za-z0-9_\-\.].
func isWordByte(c byte) bool {
	return isDigit(c) || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' || c == '-' || c == '.'
}

// StripTerminalFormatting removes what the terminal format adds to a line, its
// escape sequences and the \r of its \r\n line ends, for a copy of the log
// that is read as a file (--log-file, --terminal-log-file).
func StripTerminalFormatting(s string) string {
	return strings.ReplaceAll(terminalEscapeSequences.ReplaceAllString(s, ""), "\r\n", "\n")
}

// --- Plain handler ---

// plainHandler is TS createPlainLog: logEventToFileText (spec-utils/log.ts),
// used when stdout is not a terminal.
type plainHandler struct {
	w        io.Writer
	minLevel Level
}

// newPlainHandler's minLevel is never 0: New defaults it to info.
func newPlainHandler(w io.Writer, minLevel Level) Handler {
	return &plainHandler{w: w, minLevel: minLevel}
}

func (h *plainHandler) HandleEvent(e Event) {
	// A progress event has no level (0, below any minLevel), so it is never
	// printed, as in TS.
	if e.Level < h.minLevel {
		return
	}
	// new Date(e.timestamp).toISOString()
	stamp := "[" + time.UnixMilli(e.Timestamp).UTC().Format("2006-01-02T15:04:05.000Z") + "] "
	var line string
	switch e.Type {
	case "text", "raw":
		line = stamp + toLogFileText(e.Text)
	case "start":
		line = stamp + "Start: " + toLogFileText(e.Text)
	case "stop":
		// TS shows a stop line in the plain log at debug and trace.
		if h.minLevel <= LevelDebug {
			line = stamp + fmt.Sprintf("Stop (%d ms): ", e.Timestamp-e.StartTimestamp) + toLogFileText(e.Text)
		}
	}
	if line != "" {
		fmt.Fprint(h.w, line)
	}
}

// toLogFileText is TS toLogFileText: the text without escape sequences, its
// last line end replaced with os.EOL (one added when it has none).
func toLogFileText(text string) string {
	s := terminalEscapeSequences.ReplaceAllString(text, "")
	if t, ok := strings.CutSuffix(s, "\r\n"); ok {
		return t + eol
	}
	return strings.TrimSuffix(s, "\n") + eol
}

// --- JSON handler ---

type jsonHandler struct {
	w io.Writer
}

func newJSONHandler(w io.Writer) Handler {
	return &jsonHandler{w: w}
}

func (h *jsonHandler) HandleEvent(e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return // an Event is always serializable; drop rather than emit a broken line
	}
	fmt.Fprintf(h.w, "%s\n", data)
}

// --- Helpers ---

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
