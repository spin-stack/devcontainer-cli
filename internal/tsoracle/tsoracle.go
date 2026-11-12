package tsoracle

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type File struct {
	Comment string `json:"comment,omitempty"`
	Cases   []Case `json:"cases"`
}

type Case struct {
	Name       string            `json:"name,omitempty"`
	Fn         string            `json:"fn"`
	Args       []json.RawMessage `json:"args"`
	Divergence string            `json:"divergence,omitempty"`
	Result     json.RawMessage   `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
}

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
