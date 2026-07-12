// Package jsonc parses JSON with comments and trailing commas (JSONC), as used by devcontainer.json.
package jsonc

import (
	"bytes"
	"encoding/json"

	"github.com/tailscale/hujson"
)

// utf8BOM is the UTF-8 byte order mark some editors (notably on Windows)
// prepend. hujson rejects it, while the Node CLI accepts such files, so we
// strip a leading BOM before parsing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}

// Unmarshal parses JSONC (JSON with comments and trailing commas) into v.
func Unmarshal(data []byte, v any) error {
	std, err := hujson.Standardize(stripBOM(data))
	if err != nil {
		return err
	}
	return json.Unmarshal(std, v)
}

// Parse parses JSONC into a generic map.
func Parse(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// StripComments removes JSONC comments and trailing commas,
// returning valid standard JSON bytes.
func StripComments(data []byte) ([]byte, error) {
	return hujson.Standardize(stripBOM(data))
}
