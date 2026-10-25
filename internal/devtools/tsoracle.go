package devtools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"

	"github.com/devcontainers/cli/internal/tsoracle"
)

// TSOracleGoldens lists every internal/**/testdata/ts-oracle/*.json of fsys
// (the repository root), sorted.
func TSOracleGoldens(fsys fs.FS) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, "internal", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Ext(p) == ".json" && path.Base(path.Dir(p)) == "ts-oracle" &&
			path.Base(path.Dir(path.Dir(p))) == "testdata" {
			files = append(files, p)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

// RefreshTSOracleGoldens sends the cases of every golden (path -> content) to
// oracle in one request and returns each golden rewritten with the results
// oracle recorded. oracle reads and writes the tools/tsoracle protocol:
// {"cases":[{"fn","args"}]} in, the same cases with "result" or "error" out.
func RefreshTSOracleGoldens(goldens map[string][]byte, oracle func(request []byte) ([]byte, error)) (map[string][]byte, error) {
	paths := slices.Sorted(maps.Keys(goldens))
	type call struct {
		Fn   string            `json:"fn"`
		Args []json.RawMessage `json:"args"`
	}
	var request struct {
		Cases []call `json:"cases"`
	}
	request.Cases = []call{}
	files := make([]*tsoracle.File, len(paths))
	for i, p := range paths {
		f, err := tsoracle.Decode(goldens[p])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		files[i] = f
		for _, c := range f.Cases {
			args := c.Args
			if args == nil {
				args = []json.RawMessage{}
			}
			request.Cases = append(request.Cases, call{Fn: c.Fn, Args: args})
		}
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(request); err != nil {
		return nil, err
	}
	out, err := oracle(bytes.TrimSuffix(body.Bytes(), []byte("\n")))
	if err != nil {
		return nil, err
	}
	var response struct {
		Cases []struct {
			Fn     string          `json:"fn"`
			Result json.RawMessage `json:"result"`
			Error  *string         `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("oracle response: %w", err)
	}
	if len(response.Cases) != len(request.Cases) {
		return nil, fmt.Errorf("oracle answered %d cases, %d were sent", len(response.Cases), len(request.Cases))
	}
	refreshed := make(map[string][]byte, len(paths))
	next := 0
	for i, p := range paths {
		for j := range files[i].Cases {
			c, r := &files[i].Cases[j], response.Cases[next]
			next++
			if r.Fn != c.Fn {
				return nil, fmt.Errorf("%s: case %d: oracle answered %s for %s", p, j, r.Fn, c.Fn)
			}
			switch {
			case r.Error != nil:
				c.Result, c.Error = nil, *r.Error
			case r.Result != nil:
				c.Result, c.Error = r.Result, ""
			default:
				return nil, fmt.Errorf("%s: case %d: oracle recorded neither a result nor an error", p, j)
			}
		}
		data, err := tsoracle.Encode(files[i])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		refreshed[p] = data
	}
	return refreshed, nil
}
