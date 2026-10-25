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

// StripComments removes JSONC comments and trailing commas,
// returning valid standard JSON bytes.
func StripComments(data []byte) ([]byte, error) {
	return hujson.Standardize(stripBOM(data))
}

// MemberNames returns the names of the members of the object at field of the
// top-level JSONC object, in the order the document lists them; nil when the
// document or the field is not an object.
func MemberNames(data []byte, field string) []string {
	v, err := hujson.Parse(stripBOM(data))
	if err != nil {
		return nil
	}
	top, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil
	}
	for _, m := range top.Members {
		if lit, ok := m.Name.Value.(hujson.Literal); ok && lit.String() == field {
			obj, ok := m.Value.Value.(*hujson.Object)
			if !ok {
				return nil
			}
			names := make([]string, 0, len(obj.Members))
			for _, f := range obj.Members {
				if lit, ok := f.Name.Value.(hujson.Literal); ok {
					names = append(names, lit.String())
				}
			}
			return names
		}
	}
	return nil
}
