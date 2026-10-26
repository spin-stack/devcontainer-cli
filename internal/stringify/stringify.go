// Package stringify encodes values as JavaScript's JSON.stringify does, so the
// CLI's JSON (command results, --log-format json events) is byte-compatible
// with the TS CLI's.
package stringify

import "encoding/json"

// escapes are the escapes encoding/json writes that JSON.stringify does not: it
// writes &, <, > and U+2028/U+2029 as they are.
var escapes = map[string]string{
	"\\u0026": "&",
	"\\u003c": "<",
	"\\u003e": ">",
	"\\u2028": "\u2028",
	"\\u2029": "\u2029",
}

// JSON encodes v as JSON.stringify(v) would. encoding/json HTML-escapes every
// string, including the ones nested json.Marshaler implementations write, so
// the escapes are undone on the encoded document. A non-empty indent indents
// it as JSON.stringify(v, undefined, indent) does.
func JSON(v any, indent string) ([]byte, error) {
	var data []byte
	var err error
	if indent == "" {
		data, err = json.Marshal(v)
	} else {
		data, err = json.MarshalIndent(v, "", indent)
	}
	if err != nil {
		return nil, err
	}
	return unescape(data), nil
}

// unescape rewrites, in encoded JSON, the escapes in escapes as the characters
// they stand for. A backslash only occurs in a string, always as the start of
// an escape, so taking escapes in pairs leaves an escaped backslash followed by
// "u0026" alone.
func unescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			out = append(out, data[i])
			continue
		}
		if s, ok := escapes[string(data[i:min(i+6, len(data))])]; ok {
			out = append(out, s...)
			i += 5
			continue
		}
		out = append(out, data[i], data[i+1])
		i++
	}
	return out
}
