package oracletest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle"
)

var ErrThrows = errors.New("throws")

type OptionalString string

type Truthiness bool

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return true
	}
}

type Func func(args []json.RawMessage) (any, error)

func Run(t *testing.T, path string, impls map[string]Func) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := tsoracle.Decode(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for i, c := range f.Cases {
		impl, ok := impls[c.Fn]
		if !ok {
			t.Fatalf("%s: case %d calls %s, which the test does not map to its Go port", path, i, c.Fn)
		}
		if impl == nil {
			continue
		}
		t.Run(cmp.Or(c.Name, c.Fn+"#"+strconv.Itoa(i)), func(t *testing.T) {
			if c.Result == nil && c.Error == "" {
				t.Fatal("no recorded result: run `go tool task testdata:ts-oracle`")
			}
			if msg := verdict(c, check(impl, c)); msg != "" {
				t.Error(msg)
			}
		})
	}
}

func verdict(c tsoracle.Case, mismatch string) string {
	switch {
	case c.Divergence != "" && mismatch == "":
		return fmt.Sprintf("%s(%s) now matches TS; drop the divergence %q", c.Fn, argsText(c), c.Divergence)
	case c.Divergence == "" && mismatch != "":
		return fmt.Sprintf("%s(%s): %s", c.Fn, argsText(c), mismatch)
	}
	return ""
}

func check(impl Func, c tsoracle.Case) string {
	got, err := impl(c.Args)
	if c.Error != "" {
		switch {
		case err == nil:
			return fmt.Sprintf("Go returned %s, TS threw %q", jsonText(got), c.Error)
		case !errors.Is(err, ErrThrows) && err.Error() != c.Error:
			return fmt.Sprintf("Go error %q, TS error %q", err, c.Error)
		}
		return ""
	}
	if err != nil {
		return fmt.Sprintf("Go failed with %v, TS returned %s", err, bytes.Join(bytes.Fields(c.Result), nil))
	}
	var want, have any
	if err := json.Unmarshal(c.Result, &want); err != nil {
		return "bad recorded result: " + err.Error()
	}
	if got == OptionalString("") && (want == nil || want == "") {
		return ""
	}
	if b, ok := got.(Truthiness); ok {
		if bool(b) == truthy(want) {
			return ""
		}
		return fmt.Sprintf("Go returned %v, TS returned %s (truthy: %v)", b, jsonText(want), truthy(want))
	}
	if err := json.Unmarshal([]byte(jsonText(got)), &have); err != nil {
		return "Go result is no JSON: " + err.Error()
	}
	if !reflect.DeepEqual(want, have) {
		return fmt.Sprintf("Go returned %s, TS returned %s", jsonText(got), jsonText(want))
	}
	return ""
}

func jsonText(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return string(bytes.TrimSpace(buf.Bytes()))
}

func argsText(c tsoracle.Case) string {
	parts := make([][]byte, len(c.Args))
	for i, a := range c.Args {
		var buf bytes.Buffer
		if json.Compact(&buf, a) != nil {
			buf.Reset()
			buf.Write(a)
		}
		parts[i] = buf.Bytes()
	}
	return string(bytes.Join(parts, []byte(", ")))
}

func Args(args []json.RawMessage, ptrs ...any) error {
	if len(args) > len(ptrs) {
		return fmt.Errorf("%d arguments, the adapter reads %d", len(args), len(ptrs))
	}
	for i, a := range args {
		if err := json.Unmarshal(a, ptrs[i]); err != nil {
			return fmt.Errorf("argument %d: %w", i, err)
		}
	}
	return nil
}
