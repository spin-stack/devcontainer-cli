package features

import "testing"

func TestClassifyIDReadsOneCharacterNames(t *testing.T) {
	for _, tt := range []struct {
		id   string
		want SourceType
	}{
		{"a/b", SourceOCI},
		{"a/b:2", SourceOCI},
		{"a:2", SourceLegacyShorthand},
		{"a", SourceLegacyShorthand},
	} {
		if got := ClassifyID(tt.id); got != tt.want {
			t.Errorf("ClassifyID(%q) = %d, want %d", tt.id, got, tt.want)
		}
		if got, warning := BackwardCompatibleID(tt.id); got != tt.id || warning != "" {
			t.Errorf("BackwardCompatibleID(%q) = %q, %q; want it unchanged", tt.id, got, warning)
		}
	}
}
