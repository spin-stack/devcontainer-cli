package cli

import (
	"strings"
	"testing"
)

// A body up to the cap is read whole; one byte more is an error, never a
// truncated tarball.
func TestReadCappedFailsInsteadOfTruncating(t *testing.T) {
	data, err := readCapped(strings.NewReader("12345"), 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("readCapped at the cap = %q, %v; want the whole body", data, err)
	}
	if data, err := readCapped(strings.NewReader("123456"), 5); err == nil {
		t.Fatalf("readCapped past the cap = %q, want an error", data)
	}
	if _, err := readCapped(strings.NewReader(strings.Repeat("x", 2<<20+1)), 2<<20); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Errorf("the error = %v, want it to name the cap in MiB", err)
	}
}
