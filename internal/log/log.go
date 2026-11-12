package log

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/devcontainers/cli/internal/stringify"
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

type Event struct {
	Type           string `json:"type"`
	Level          Level  `json:"level,omitempty"`
	Timestamp      int64  `json:"timestamp,omitempty"`
	Text           string `json:"text,omitempty"`
	StartTimestamp int64  `json:"startTimestamp,omitempty"`
	Name           string `json:"name,omitempty"`
	Status         string `json:"status,omitempty"`
	StepDetail     string `json:"stepDetail,omitempty"`
	Channel        string `json:"channel,omitempty"`
}

type Logger interface {
	Write(text string, level ...Level)
	Raw(text string, level ...Level)
	Start(text string, level ...Level) int64
	Stop(text string, startTimestamp int64, level ...Level)
	Event(e Event)
}

type Handler interface {
	HandleEvent(e Event)
}

type Options struct {
	Level     Level
	Format    string
	Writer    io.Writer
	Version   string
	StartTime time.Time
	Secrets   []string
}

type logger struct {
	handler      Handler
	defaultLevel Level
	secrets      []string
}

func New(opts Options) Logger {
	if opts.Writer == nil {
		opts.Writer = os.Stderr
	}
	if opts.StartTime.IsZero() {
		opts.StartTime = time.Now()
	}

	if opts.Level == 0 {
		opts.Level = LevelInfo
	}

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

func BannerLine(version string) string {
	platform := runtime.GOOS
	if rel := osRelease(); rel != "" {
		platform += " " + rel
	}
	return fmt.Sprintf("@devcontainers/cli %s. Go %s. %s %s.", version, runtime.Version(), platform, runtime.GOARCH)
}

func osRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

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

var Null Logger = &nullLog{}

type nullLog struct{}

func (n *nullLog) Write(string, ...Level)       {}
func (n *nullLog) Raw(string, ...Level)         {}
func (n *nullLog) Start(string, ...Level) int64 { return nowMillis() }
func (n *nullLog) Stop(string, int64, ...Level) {}
func (n *nullLog) Event(Event)                  {}

// stdoutIsTerminal reports what TS reads as process.stdout.isTTY: whether the
// CLI's stdout is a terminal. Like TS, the choice of log format follows stdout
// even though the log itself goes to stderr.
func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

type terminalHandler struct {
	w        io.Writer
	start    int64
	minLevel Level
}

func newTerminalHandler(w io.Writer, start time.Time, minLevel Level) Handler {
	return &terminalHandler{w: w, start: start.UnixMilli(), minLevel: minLevel}
}

func (h *terminalHandler) HandleEvent(e Event) {
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
		if trace {
			line = color(stopColor, "["+elapsed+"] Stop") + fmt.Sprintf(" (%d ms): ", e.Timestamp-e.StartTimestamp) + toTerminalText(e.Text)
		}
	}
	if line != "" {
		fmt.Fprint(h.w, line)
	}
}

const (
	stopColor      = "38;2;143;99;79"
	startColor     = "38;2;99;143;79"
	timestampColor = "38;2;99;143;79"
	numberColor    = "38;2;86;156;214"
)

func ToErrorText(s string) string {
	lines := newlines.Split(s, -1)
	for i, l := range lines {
		lines[i] = color("31", l)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func color(c, s string) string {
	return "\x1b[1m\x1b[" + c + "m" + s + "\x1b[39m\x1b[22m"
}

var terminalEscapeSequences = regexp.MustCompile(`(\x9B|\x1B\[)[0-?]*[ -/]*[@-~]`)

var newlines = regexp.MustCompile(`\r?\n`)

func toTerminalText(text string) string {
	s := newlines.ReplaceAllString(colorize(text), "\r\n")
	if !strings.HasSuffix(s, "\r\n") {
		s += "\r\n"
	}
	return s
}

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

func isWordByte(c byte) bool {
	return isDigit(c) || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' || c == '-' || c == '.'
}

func StripTerminalFormatting(s string) string {
	return strings.ReplaceAll(terminalEscapeSequences.ReplaceAllString(s, ""), "\r\n", "\n")
}

type plainHandler struct {
	w        io.Writer
	minLevel Level
}

func newPlainHandler(w io.Writer, minLevel Level) Handler {
	return &plainHandler{w: w, minLevel: minLevel}
}

func (h *plainHandler) HandleEvent(e Event) {
	if e.Level < h.minLevel {
		return
	}
	stamp := "[" + time.UnixMilli(e.Timestamp).UTC().Format("2006-01-02T15:04:05.000Z") + "] "
	var line string
	switch e.Type {
	case "text", "raw":
		line = stamp + toLogFileText(e.Text)
	case "start":
		line = stamp + "Start: " + toLogFileText(e.Text)
	case "stop":
		if h.minLevel <= LevelDebug {
			line = stamp + fmt.Sprintf("Stop (%d ms): ", e.Timestamp-e.StartTimestamp) + toLogFileText(e.Text)
		}
	}
	if line != "" {
		fmt.Fprint(h.w, line)
	}
}

func toLogFileText(text string) string {
	s := terminalEscapeSequences.ReplaceAllString(text, "")
	if t, ok := strings.CutSuffix(s, "\r\n"); ok {
		return t + eol
	}
	return strings.TrimSuffix(s, "\n") + eol
}

type jsonHandler struct {
	w io.Writer
}

func newJSONHandler(w io.Writer) Handler {
	return &jsonHandler{w: w}
}

func (h *jsonHandler) HandleEvent(e Event) {
	data, err := stringify.JSON(e, "")
	if err != nil {
		return
	}
	fmt.Fprintf(h.w, "%s\n", data)
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
