package imagemeta

import "testing"

func TestSafeIDAsTSGetSafeIdWritesIt(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Go1-x", "GO1_X"},
		{"1abc", "_ABC"},
		{"_abc", "_ABC"},
		{"9", "_"},
		{"é-z", "_Z"},
		{"a￿b", "A_B"},
		{"a\U00010000b", "A__B"},
		{"a😀b", "A__B"},
	} {
		if got := safeID(tt.in); got != tt.want {
			t.Errorf("safeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
