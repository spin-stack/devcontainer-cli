package devtools

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTSOracleGoldens(t *testing.T) {
	fsys := fstest.MapFS{
		"internal/b/testdata/ts-oracle/x.json":     {},
		"internal/a/testdata/ts-oracle/y.json":     {},
		"internal/a/testdata/ts-oracle/notes.txt":  {},
		"internal/a/testdata/other/z.json":         {},
		"internal/a/ts-oracle/w.json":              {},
		"internal/a/testdata/ts-oracle/sub/v.json": {},
	}
	got, err := TSOracleGoldens(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/a/testdata/ts-oracle/y.json", "internal/b/testdata/ts-oracle/x.json"}
	if !slices.Equal(got, want) {
		t.Errorf("goldens = %v, want %v", got, want)
	}
}

// fakeOracle answers parseVersion with its argument's length and throws for
// "boom", recording the request it was sent.
func fakeOracle(sent *[]byte) func([]byte) ([]byte, error) {
	return func(req []byte) ([]byte, error) {
		*sent = req
		var in struct {
			Cases []struct {
				Fn   string `json:"fn"`
				Args []any  `json:"args"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, c := range in.Cases {
			r := map[string]any{"fn": c.Fn, "args": c.Args}
			if c.Fn == "boom" {
				r["error"] = "it threw"
			} else {
				r["result"] = len(c.Args[0].(string))
			}
			out = append(out, r)
		}
		return json.Marshal(map[string]any{"cases": out})
	}
}

func TestRefreshTSOracleGoldens(t *testing.T) {
	goldens := map[string][]byte{
		"internal/b/testdata/ts-oracle/b.json": []byte(`{"cases":[{"fn":"boom","args":["x"],"result":1}]}`),
		"internal/a/testdata/ts-oracle/a.json": []byte(`{"comment":"kept","cases":[
			{"name":"n","fn":"len","args":["<&>"],"divergence":"why","error":"stale"},
			{"fn":"len","args":["ab"]}]}`),
	}
	var sent []byte
	got, err := RefreshTSOracleGoldens(goldens, fakeOracle(&sent))
	if err != nil {
		t.Fatal(err)
	}
	// One request, the files' cases in path order, only fn and args.
	if want := `{"cases":[{"fn":"len","args":["<&>"]},{"fn":"len","args":["ab"]},{"fn":"boom","args":["x"]}]}`; string(sent) != want {
		t.Errorf("request = %s, want %s", sent, want)
	}
	wantA := `{
  "comment": "kept",
  "cases": [
    {
      "name": "n",
      "fn": "len",
      "args": [
        "<&>"
      ],
      "divergence": "why",
      "result": 3
    },
    {
      "fn": "len",
      "args": [
        "ab"
      ],
      "result": 2
    }
  ]
}
`
	if s := string(got["internal/a/testdata/ts-oracle/a.json"]); s != wantA {
		t.Errorf("a.json =\n%s\nwant\n%s", s, wantA)
	}
	if s := string(got["internal/b/testdata/ts-oracle/b.json"]); !strings.Contains(s, `"error": "it threw"`) || strings.Contains(s, `"result"`) {
		t.Errorf("b.json = %s, want the thrown error in place of the stale result", s)
	}
	// Refreshing a refreshed golden changes nothing.
	again, err := RefreshTSOracleGoldens(got, fakeOracle(&sent))
	if err != nil {
		t.Fatal(err)
	}
	for p := range got {
		if string(again[p]) != string(got[p]) {
			t.Errorf("%s is not stable:\n%s\nthen\n%s", p, got[p], again[p])
		}
	}
}

func TestRefreshTSOracleGoldensNullResult(t *testing.T) {
	goldens := map[string][]byte{"g.json": []byte(`{"cases":[{"fn":"f","args":[]}]}`)}
	got, err := RefreshTSOracleGoldens(goldens, func([]byte) ([]byte, error) {
		return []byte(`{"cases":[{"fn":"f","args":[],"result":null}]}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got["g.json"]), `"result": null`) {
		t.Errorf("g.json = %s, want the null result recorded", got["g.json"])
	}
}

func TestRefreshTSOracleGoldensRejects(t *testing.T) {
	oracleErr := errors.New("node failed")
	tests := []struct {
		name    string
		golden  string
		answer  string
		err     error
		wantErr string
	}{
		{"bad golden", `{"cases":[{"args":[]}]}`, ``, nil, "no fn"},
		{"unknown golden field", `{"cases":[],"extra":1}`, ``, nil, "unknown field"},
		{"oracle fails", `{"cases":[{"fn":"f","args":[]}]}`, ``, oracleErr, "node failed"},
		{"missing case", `{"cases":[{"fn":"f","args":[]}]}`, `{"cases":[]}`, nil, "answered 0 cases, 1 were sent"},
		{"other function", `{"cases":[{"fn":"f","args":[]}]}`, `{"cases":[{"fn":"g","result":1}]}`, nil, "answered g for f"},
		{"no result", `{"cases":[{"fn":"f","args":[]}]}`, `{"cases":[{"fn":"f"}]}`, nil, "neither a result nor an error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RefreshTSOracleGoldens(map[string][]byte{"g.json": []byte(tc.golden)}, func([]byte) ([]byte, error) {
				return []byte(tc.answer), tc.err
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// With no cases the oracle is still asked with a cases array, which the
// tools/tsoracle protocol requires, not with null.
func TestRefreshTSOracleGoldensAsksWithAnEmptyCasesArray(t *testing.T) {
	var asked string
	_, err := RefreshTSOracleGoldens(map[string][]byte{"g.json": []byte(`{"cases":[]}`)}, func(request []byte) ([]byte, error) {
		asked = string(request)
		return []byte(`{"cases":[]}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != `{"cases":[]}` {
		t.Errorf("request = %s, want an empty cases array", asked)
	}
}
