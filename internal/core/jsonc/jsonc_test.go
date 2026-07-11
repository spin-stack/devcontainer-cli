package jsonc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnmarshal_Basic(t *testing.T) {
	input := []byte(`{"image": "ubuntu", "features": {}}`)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_UTF8BOM(t *testing.T) {
	// Editors on Windows may prepend a UTF-8 BOM; the Node CLI accepts it.
	input := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"image": "ubuntu"}`)...)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatalf("BOM-prefixed JSONC should parse: %v", err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_LineComment(t *testing.T) {
	input := []byte(`{
		// this is a comment
		"image": "ubuntu"
	}`)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_BlockComment(t *testing.T) {
	input := []byte(`{"image": "ubuntu" /* block comment */}`)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_TrailingComma(t *testing.T) {
	input := []byte(`{"image": "ubuntu", "features": {},}`)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
	}
}

func TestUnmarshal_CommentBetweenProperties(t *testing.T) {
	input := []byte(`{"image": "ubuntu", /* inline */ "features": {}}`)
	var m map[string]any
	if err := Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image = %v", m["image"])
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

func TestUnmarshal_InvalidJSON(t *testing.T) {
	input := []byte(`{not json`)
	var m map[string]any
	if err := Unmarshal(input, &m); err == nil {
		t.Error("expected error for invalid JSON")
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
