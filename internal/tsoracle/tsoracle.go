// Package tsoracle is the format of the testdata/ts-oracle/*.json goldens: calls
// of reference TypeScript functions with the result the real function returned,
// recorded by tools/tsoracle (cmd/devtool ts-oracle). Tests assert the Go ports
// against them with oracletest.Run; the goldens are regenerated, never typed by
// hand.
package tsoracle

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// File is one golden document.
type File struct {
	// Comment says what the file covers; it is kept as written.
	Comment string `json:"comment,omitempty"`
	Cases   []Case `json:"cases"`
}

// Case is a call of the TS function Fn with Args. Name, Fn, Args and
// Divergence are written by hand; Result or Error is the oracle's.
type Case struct {
	Name string            `json:"name,omitempty"`
	Fn   string            `json:"fn"`
	Args []json.RawMessage `json:"args"`
	// Divergence, when set, is why the Go port returns something else
	// (docs/DIVERGENCES.md, or a divergence not resolved yet); oracletest.Run
	// then requires the results to differ.
	Divergence string `json:"divergence,omitempty"`
	// Result is what the function returned (null for undefined).
	Result json.RawMessage `json:"result,omitempty"`
	// Error is the message of what the function threw.
	Error string `json:"error,omitempty"`
}

// Decode reads a golden document.
func Decode(data []byte) (*File, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	for i, c := range f.Cases {
		if c.Fn == "" {
			return nil, fmt.Errorf("case %d has no fn", i)
		}
	}
	return &f, nil
}

// Encode writes a golden document deterministically: two-space indentation,
// no HTML escaping, a trailing newline.
func Encode(f *File) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
