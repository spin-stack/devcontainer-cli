package features

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

// jsObject is a JavaScript object as JSON.parse builds one: a property set
// again keeps its first position and takes the last value, and JSON.stringify
// writes the integer-index properties first, ascending, then the others in
// the order they were added. Its values are jsObject, []any, string, float64,
// bool or nil.
type jsObject struct {
	keys []string
	vals map[string]any
}

func newJSObject() *jsObject { return &jsObject{vals: map[string]any{}} }

func (o *jsObject) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsObject) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *jsObject) remove(k string) {
	if _, ok := o.vals[k]; ok {
		delete(o.vals, k)
		o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
	}
}

func (o *jsObject) spread(src *jsObject) *jsObject {
	out := &jsObject{keys: slices.Clone(o.keys), vals: make(map[string]any, len(o.vals))}
	for k, v := range o.vals {
		out.vals[k] = v
	}
	for _, k := range src.keys {
		out.set(k, src.vals[k])
	}
	return out
}

// isArrayIndex reports whether k is an array index (a canonical integer
// below 2^32-1), which JS orders first.
func isArrayIndex(k string) bool {
	n, err := strconv.ParseUint(k, 10, 32)
	return err == nil && n != 1<<32-1 && strconv.FormatUint(n, 10) == k
}

func (o *jsObject) order() []string {
	var idx, rest []string
	for _, k := range o.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortFunc(idx, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
	})
	return append(idx, rest...)
}

// KeyOrder lists the distinct keys of an object that JavaScript gave the
// properties keys, in that order, as it enumerates them: the integer-index
// keys ascending, then the others where each was first set.
func KeyOrder(keys []string) []string {
	o := newJSObject()
	for _, k := range keys {
		o.set(k, nil)
	}
	return o.order()
}

// PropertyOrder is KeyOrder of m's keys as declared, followed by the keys of
// m that declared does not name (a map a caller built), sorted.
func PropertyOrder[V any](m map[string]V, declared []string) []string {
	var keys []string
	for _, k := range declared {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
		}
	}
	return KeyOrder(append(keys, slices.Sorted(maps.Keys(m))...))
}

func (o *jsObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.order() {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func parseJS(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	v, err := parseJSValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("parse JSON: data after the value")
	}
	return v, nil
}

func parseJSValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		obj := newJSObject()
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseJSValue(dec)
			if err != nil {
				return nil, err
			}
			obj.set(k.(string), v)
		}
		_, err := dec.Token()
		return obj, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := parseJSValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	return tok, nil
}

func toJS(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return parseJS(data)
}

// JSValue is toJS of v, with the keys of a map v in
// PropertyOrder(v, declared).
func JSValue(v any, declared []string) (any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return toJS(v)
	}
	obj := newJSObject()
	for _, k := range PropertyOrder(m, declared) {
		js, err := toJS(m[k])
		if err != nil {
			return nil, err
		}
		obj.set(k, js)
	}
	return obj, nil
}

func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

func spreadSource(v any) *jsObject {
	out := newJSObject()
	switch x := v.(type) {
	case *jsObject:
		return x
	case []any:
		for i, e := range x {
			out.set(strconv.Itoa(i), e)
		}
	case string:
		// A string spreads into its UTF-16 code units; half of a surrogate
		// pair, which a Go string cannot hold, becomes U+FFFD.
		for i, u := range utf16.Encode([]rune(x)) {
			out.set(strconv.Itoa(i), string(utf16.Decode([]uint16{u})))
		}
	}
	return out
}
