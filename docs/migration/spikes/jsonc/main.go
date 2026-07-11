package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

func main() {
	configsDir := "/Users/aledbf/Trabajo/github/cli/src/test/configs"

	// Track results
	var passed, failed int
	var failures []string

	fmt.Println("=== Fixture files ===")
	fmt.Println()

	err := filepath.Walk(configsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(path) != "devcontainer.json" {
			return nil
		}

		relPath, _ := filepath.Rel(configsDir, path)
		result := testFile(path)
		if result == nil {
			fmt.Printf("  PASS  %s\n", relPath)
			passed++
		} else {
			fmt.Printf("  FAIL  %s\n        error: %v\n", relPath, result)
			failed++
			failures = append(failures, relPath)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("=== Edge cases ===")
	fmt.Println()

	edgeCases := []struct {
		name  string
		input string
	}{
		{
			name:  "block comment after value",
			input: `{"image": "ubuntu" /* block comment */}`,
		},
		{
			name:  "line comment after closing brace",
			input: `{"image": "ubuntu"} // line comment`,
		},
		{
			name:  "trailing comma in object",
			input: `{"image": "ubuntu", "features": {},}`,
		},
		{
			name:  "comment between properties",
			input: `{"image": "ubuntu", /* comment in array */ "features": {}}`,
		},
	}

	for _, tc := range edgeCases {
		result := testBytes([]byte(tc.input))
		if result == nil {
			fmt.Printf("  PASS  %s\n", tc.name)
			passed++
		} else {
			fmt.Printf("  FAIL  %s\n        input: %s\n        error: %v\n", tc.name, tc.input, result)
			failed++
			failures = append(failures, "edge-case: "+tc.name)
		}
	}

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Println()
	fmt.Printf("  Passed: %d\n", passed)
	fmt.Printf("  Failed: %d\n", failed)

	if failed > 0 {
		fmt.Println()
		fmt.Println("  Failures:")
		for _, f := range failures {
			fmt.Printf("    - %s\n", f)
		}
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("  All tests passed!")
}

func testFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return testBytes(data)
}

func testBytes(data []byte) error {
	// hujson.Standardize strips comments and trailing commas,
	// producing valid JSON.
	standardized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("hujson.Standardize: %w", err)
	}

	// Verify the result is valid JSON by unmarshalling.
	var result map[string]interface{}
	if err := json.Unmarshal(standardized, &result); err != nil {
		// Some edge cases may produce a top-level value that isn't an object
		// after stripping comments (e.g. line comment after closing brace).
		// Try a more lenient unmarshal.
		var anything interface{}
		if err2 := json.Unmarshal(standardized, &anything); err2 != nil {
			return fmt.Errorf("json.Unmarshal: %w (standardized: %s)", err2, strings.TrimSpace(string(standardized)))
		}
	}

	return nil
}
