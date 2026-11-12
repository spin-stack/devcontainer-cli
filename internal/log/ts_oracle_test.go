package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/tsoracle/oracletest"
)

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

func written(s string) any {
	if s == "" {
		return nil
	}
	return s
}
