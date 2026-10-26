package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

// The goldens are the lines the reference log handlers write for one event
// (go tool task testdata:ts-oracle): the terminal log when stdout is a TTY, the
// plain log otherwise, and the JSON log for --log-format json.
func TestTSOracleLog(t *testing.T) {
	oracletest.Run(t, "testdata/ts-oracle/log.json", map[string]oracletest.Func{
		"logEventToTerminalText": func(args []json.RawMessage) (any, error) {
			var e Event
			var level Level
			var start int64
			if err := oracletest.Args(args, &e, &level, &start); err != nil {
				return nil, err
			}
			var buf bytes.Buffer
			newTerminalHandler(&buf, time.UnixMilli(start), level).HandleEvent(e)
			return written(buf.String()), nil
		},
		"plainLogText": func(args []json.RawMessage) (any, error) {
			var e Event
			var level Level
			if err := oracletest.Args(args, &e, &level); err != nil {
				return nil, err
			}
			var buf bytes.Buffer
			newPlainHandler(&buf, level).HandleEvent(e)
			// The goldens are recorded where Node's os.EOL is "\n".
			return written(strings.ReplaceAll(buf.String(), eol, "\n")), nil
		},
		"jsonLogText": func(args []json.RawMessage) (any, error) {
			var e Event
			if err := oracletest.Args(args, &e); err != nil {
				return nil, err
			}
			var buf bytes.Buffer
			newJSONHandler(&buf).HandleEvent(e)
			return written(buf.String()), nil
		},
	})
}

// written is what a handler wrote, or nil (TS undefined) when it wrote nothing.
func written(s string) any {
	if s == "" {
		return nil
	}
	return s
}
