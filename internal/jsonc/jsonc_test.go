package jsonc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantErr   bool
		wantImage any
	}{
		{
			name:      "Basic",
			input:     []byte(`{"image": "ubuntu", "features": {}}`),
			wantImage: "ubuntu",
		},
		{
			// Editors on Windows may prepend a UTF-8 BOM; the Node CLI accepts it.
			name:      "UTF8BOM",
			input:     append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"image": "ubuntu"}`)...),
			wantImage: "ubuntu",
		},
		{
			name: "LineComment",
			input: []byte(`{
		// this is a comment
		"image": "ubuntu"
	}`),
			wantImage: "ubuntu",
		},
		{
			name:      "BlockComment",
			input:     []byte(`{"image": "ubuntu" /* block comment */}`),
			wantImage: "ubuntu",
		},
		{
			name:      "TrailingComma",
			input:     []byte(`{"image": "ubuntu", "features": {},}`),
			wantImage: "ubuntu",
		},
		{
			name:      "CommentBetweenProperties",
			input:     []byte(`{"image": "ubuntu", /* inline */ "features": {}}`),
			wantImage: "ubuntu",
		},
		{
			name:    "InvalidJSON",
			input:   []byte(`{not json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m map[string]any
			err := Unmarshal(tt.input, &m)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error for invalid JSON")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal should parse: %v", err)
			}
			if m["image"] != tt.wantImage {
				t.Errorf("image = %v, want %v", m["image"], tt.wantImage)
			}
		})
	}
}

func TestParse(t *testing.T) {
	input := []byte(`{"image": "ubuntu"}`)
	m, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestStripComments(t *testing.T) {
	input := []byte(`{"image": "ubuntu" /* comment */}`)
	out, err := StripComments(input)
	if err != nil {
		t.Fatal(err)
	}
	// hujson replaces comments with whitespace; verify the result is valid JSON
	var m map[string]any
	if err := Unmarshal(out, &m); err != nil {
		t.Fatalf("stripped output is not valid JSON: %v", err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_AllFixtures(t *testing.T) {
	fixturesDir := filepath.Join("..", "..", "..", "src", "test", "configs")
	if _, err := os.Stat(fixturesDir); os.IsNotExist(err) {
		t.Skipf("fixtures dir not found: %s", fixturesDir)
	}

	var count int
	err := filepath.Walk(fixturesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) != "devcontainer.json" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var m map[string]any
		if err := Unmarshal(data, &m); err != nil {
			rel, _ := filepath.Rel(fixturesDir, path)
			t.Errorf("FAIL %s: %v", rel, err)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Skip("no fixtures found")
	}
	t.Logf("parsed %d fixtures successfully", count)
}

// FuzzStandardizePreservesMeaning checks the contract that accepted JSONC is
// converted into valid JSON without changing the decoded value. The seed corpus
// includes the syntax extensions and BOM behavior accepted by devcontainer.json.
func FuzzStandardizePreservesMeaning(f *testing.F) {
	f.Add([]byte(`{"image":"ubuntu"}`))
	f.Add([]byte("\xef\xbb\xbf{\"image\":\"ubuntu\",}"))
	f.Add([]byte("{/* comment */\"features\":{},}"))
	f.Add([]byte(`{"nested":[1,true,null,{"x":"// not a comment"}]}`))

	f.Fuzz(func(t *testing.T, input []byte) {
		standard, err := StripComments(input)
		if err != nil {
			return
		}
		if !json.Valid(standard) {
			t.Fatalf("StripComments succeeded with invalid JSON: %q", standard)
		}

		var fromJSONC, fromStandard any
		if err := Unmarshal(input, &fromJSONC); err != nil {
			t.Fatalf("StripComments accepted input that Unmarshal rejected: %v", err)
		}
		if err := json.Unmarshal(standard, &fromStandard); err != nil {
			t.Fatalf("standard output cannot be decoded: %v", err)
		}
		if !reflect.DeepEqual(fromJSONC, fromStandard) {
			t.Fatalf("standardization changed value: JSONC=%#v JSON=%#v", fromJSONC, fromStandard)
		}
	})
}
