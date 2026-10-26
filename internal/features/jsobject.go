package features

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
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

// set is obj[k] = v.
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

// remove is delete obj[k].
func (o *jsObject) remove(k string) {
	if _, ok := o.vals[k]; ok {
		delete(o.vals, k)
		o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
	}
}

// spread is {...o, ...src} for an object src: src's properties set on a copy of o.
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

// arrayIndex reports whether k is an array index, which JS orders first.
func arrayIndex(k string) (uint64, bool) {
	n, err := strconv.ParseUint(k, 10, 32)
	if err != nil || n == 1<<32-1 || strconv.FormatUint(n, 10) != k {
		return 0, false
	}
	return n, true
}

// order is the order JSON.stringify writes o's properties in.
func (o *jsObject) order() []string {
	var idx, rest []string
	for _, k := range o.keys {
		if _, ok := arrayIndex(k); ok {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortFunc(idx, func(a, b string) int {
		x, _ := arrayIndex(a)
		y, _ := arrayIndex(b)
		return cmp.Compare(x, y)
	})
	return append(idx, rest...)
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

// parseJS is JSON.parse(data): data's value with its objects as jsObject.
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

// toJS is v as JSON.parse(JSON.stringify(v)) gives it back.
func toJS(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return parseJS(data)
}

// jsTruthy is JavaScript's truthiness of a parsed JSON value.
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

// spreadSource is the own enumerable properties {...v} copies from v: an
// object's, an array's or a string's (by index); none for other values.
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
