package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/tailscale/hujson"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

const secretsFileDescription = "Failed to read/parse secrets"

type secret struct {
	name  string
	value any
}

type fileSecrets []secret

func loadSecretsFile(path string) (fileSecrets, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &coreerrors.ContainerError{Description: secretsFileDescription, OriginalError: nodeReadFileError(path, err)}
	}
	value, err := parseJSONCStrict(data)
	if err != nil {
		return nil, &coreerrors.ContainerError{Description: secretsFileDescription, OriginalError: errors.New("Invalid json data")}
	}
	return jsSpreadEntries(value), nil
}

func nodeReadFileError(path string, err error) error {
	switch {
	case errors.Is(err, syscall.ENOENT):
		return fmt.Errorf("ENOENT: no such file or directory, open '%s'", path)
	case errors.Is(err, syscall.EACCES):
		return fmt.Errorf("EACCES: permission denied, open '%s'", path)
	case errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("ENOTDIR: not a directory, open '%s'", path)
	case errors.Is(err, syscall.EISDIR):
		return errors.New("EISDIR: illegal operation on a directory, read")
	}
	return err
}

// parseJSONCStrict accepts what jsonc-parser's parse does without reporting
// an error under its default options: JSON plus comments. Unlike
// internal/jsonc it rejects trailing commas and a leading BOM, as TS does
// for a secrets file (Buffer.toString keeps the BOM).
func parseJSONCStrict(data []byte) (any, error) {
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	for node := range v.All() {
		var last *hujson.Value
		switch c := node.Value.(type) {
		case *hujson.Object:
			if n := len(c.Members); n > 0 {
				last = &c.Members[n-1].Value
			}
		case *hujson.Array:
			if n := len(c.Elements); n > 0 {
				last = &c.Elements[n-1]
			}
		}
		if last != nil && last.AfterExtra != nil {
			return nil, errors.New("trailing comma")
		}
	}
	v.Standardize()
	dec := json.NewDecoder(bytes.NewReader(v.Pack()))
	dec.UseNumber()
	return decodeOrdered(dec)
}

func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			members := []secret{}
			for dec.More() {
				nameTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				members = append(members, secret{nameTok.(string), value})
			}
			_, err := dec.Token()
			return members, err
		case '[':
			elems := []any{}
			for dec.More() {
				value, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				elems = append(elems, value)
			}
			_, err := dec.Token()
			return elems, err
		}
	}
	return tok, nil
}

// jsSpreadEntries is the [key, value] list of `{...value}`: an object's own
// keys (a repeated key keeps its first position and its last value; array
// index keys come first, ascending), an array's indexes, a string's
// characters, and nothing for null, a number or a boolean.
func jsSpreadEntries(value any) fileSecrets {
	switch v := value.(type) {
	case []secret:
		pos := map[string]int{}
		var out fileSecrets
		for _, m := range v {
			if i, ok := pos[m.name]; ok {
				out[i].value = m.value
				continue
			}
			pos[m.name] = len(out)
			out = append(out, m)
		}
		slices.SortStableFunc(out, func(a, b secret) int {
			ai, aok := jsArrayIndex(a.name)
			bi, bok := jsArrayIndex(b.name)
			switch {
			case aok && bok:
				return compareUint(ai, bi)
			case aok:
				return -1
			case bok:
				return 1
			}
			return 0
		})
		return out
	case []any:
		out := make(fileSecrets, len(v))
		for i, e := range v {
			out[i] = secret{strconv.Itoa(i), e}
		}
		return out
	case string:
		out := fileSecrets{}
		for i, r := range []rune(v) {
			out = append(out, secret{strconv.Itoa(i), string(r)})
		}
		return out
	}
	return fileSecrets{}
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// jsArrayIndex reports whether name is a canonical array index (0 to
// 2^32-2 without leading zeros), which JS enumerates before other keys.
func jsArrayIndex(name string) (uint64, bool) {
	if name == "" || (len(name) > 1 && name[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(name, 10, 64)
	if err != nil || n > 1<<32-2 {
		return 0, false
	}
	return n, true
}

func (s fileSecrets) env() []string {
	if s == nil {
		return nil
	}
	envs := make([]string, 0, len(s))
	for _, e := range s {
		envs = append(envs, e.name+"="+jsString(e.value))
	}
	return envs
}

// maskValues is what TS maskSecrets masks: Object.values(secrets) filtered
// by `v.length`, so strings and arrays (as String(array)) are masked and
// numbers, booleans and objects are not. A null value fails there as it
// fails TS ("Cannot read properties of null (reading 'length')").
func (s fileSecrets) maskValues() ([]string, error) {
	var values []string
	for _, e := range s {
		switch v := e.value.(type) {
		case nil:
			return nil, errors.New("Cannot read properties of null (reading 'length')")
		case string:
			values = append(values, v)
		case []any:
			if len(v) > 0 {
				values = append(values, jsString(v))
			}
		}
	}
	return values, nil
}

func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case json.Number:
		// jsonc-parser reads numbers with JS Number(): one past the float64
		// range is Infinity, which ParseFloat returns along with ErrRange.
		f, _ := strconv.ParseFloat(v.String(), 64)
		switch {
		case math.IsInf(f, 1):
			return "Infinity"
		case math.IsInf(f, -1):
			return "-Infinity"
		}
		// encoding/json writes a finite float64 as JS does.
		data, _ := json.Marshal(f)
		return string(data)
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func buildSecretIDs(secrets []string) []string {
	ids := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if i := strings.IndexByte(s, '='); i > 0 {
			ids = append(ids, s[:i])
		}
	}
	return ids
}
