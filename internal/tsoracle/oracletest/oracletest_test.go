package oracletest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/tsoracle"
)

func call(fn string, result, errMsg string, args ...string) tsoracle.Case {
	c := tsoracle.Case{Fn: fn, Error: errMsg}
	if result != "" {
		c.Result = json.RawMessage(result)
	}
	for _, a := range args {
		c.Args = append(c.Args, json.RawMessage(a))
	}
	return c
}

func returns(v any, err error) Func {
	return func([]json.RawMessage) (any, error) { return v, err }
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		impl Func
		c    tsoracle.Case
		want string // a substring of the mismatch; "" for a match
	}{
		{"equal object", returns(map[string]any{"b": 1, "a": "x"}, nil), call("f", `{"a":"x","b":1}`, ""), ""},
		{"null for nil", returns(nil, nil), call("f", `null`, ""), ""},
		{"different value", returns("a&b", nil), call("f", `"a"`, ""), `Go returned "a&b", TS returned "a"`},
		{"array order matters", returns([]int{2, 1}, nil), call("f", `[1,2]`, ""), "Go returned [2,1]"},
		{"Go fails where TS returns", returns(nil, errors.New("boom")), call("f", `1`, ""), "Go failed with boom, TS returned 1"},
		{"same error", returns(nil, errors.New("bad input.")), call("f", "", "bad input."), ""},
		{"other error message", returns(nil, errors.New("bad")), call("f", "", "bad input."), `Go error "bad", TS error "bad input."`},
		{"throws, own message", returns(nil, ErrThrows), call("f", "", "bad input."), ""},
		{"Go returns where TS throws", returns(1, nil), call("f", "", "bad input."), `Go returned 1, TS threw "bad input."`},
		{"optional string for undefined", returns(OptionalString(""), nil), call("f", `null`, ""), ""},
		{"optional string for empty", returns(OptionalString(""), nil), call("f", `""`, ""), ""},
		{"optional string value", returns(OptionalString("x"), nil), call("f", `"x"`, ""), ""},
		{"optional string differs", returns(OptionalString(""), nil), call("f", `"x"`, ""), `Go returned ""`},
		{"truthy string", returns(Truthiness(true), nil), call("f", `"unknown"`, ""), ""},
		{"falsy false", returns(Truthiness(false), nil), call("f", `false`, ""), ""},
		{"truthiness differs", returns(Truthiness(true), nil), call("f", `0`, ""), "truthy: false"},
		{"truthy object", returns(Truthiness(true), nil), call("f", `{}`, ""), ""},
		{"bad recorded result", returns(1, nil), call("f", `{`, ""), "bad recorded result: unexpected end of JSON input"},
		{"Go result is no JSON", returns(func() {}, nil), call("f", `1`, ""), "Go result is no JSON: invalid character"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := check(tc.impl, tc.c)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("check = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	c := call("f", `1`, "", `{ "a" : 1 }`, `"x"`)
	if got := verdict(c, ""); got != "" {
		t.Errorf("match = %q, want no failure", got)
	}
	if got := verdict(c, "Go returned 2"); got != `f({"a":1}, "x"): Go returned 2` {
		t.Errorf("mismatch = %q", got)
	}
	c.Divergence = "why"
	if got := verdict(c, "Go returned 2"); got != "" {
		t.Errorf("recorded divergence = %q, want no failure", got)
	}
	if got := verdict(c, ""); !strings.Contains(got, `now matches TS; drop the divergence "why"`) {
		t.Errorf("resolved divergence = %q", got)
	}
}

func TestArgs(t *testing.T) {
	var s string
	var n int
	if err := Args([]json.RawMessage{json.RawMessage(`"a"`)}, &s, &n); err != nil || s != "a" || n != 0 {
		t.Errorf("Args = %q, %d, %v; want a, 0 (missing argument left alone), nil", s, n, err)
	}
	if err := Args([]json.RawMessage{json.RawMessage(`1`)}, &s); err == nil {
		t.Error("a number for a string decoded")
	}
	if err := Args([]json.RawMessage{json.RawMessage(`1`), json.RawMessage(`2`)}, &n); err == nil {
		t.Error("more arguments than targets accepted")
	}
}
