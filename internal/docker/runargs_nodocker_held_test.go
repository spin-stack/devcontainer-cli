package docker

import "testing"

// A readonly/ro key with a value is read-only for "true" and "1" and writable
// for anything else, the subset of strconv.ParseBool the Docker CLI's own
// --mount parser (opts.MountOpt) accepts for that key.
func TestParseMountSpecReadsTheReadonlyValue(t *testing.T) {
	for spec, want := range map[string]bool{
		"target=/t,readonly=true":  true,
		"target=/t,ro=1":           true,
		"target=/t,readonly=false": false,
		"target=/t,ro=0":           false,
	} {
		m, err := ParseMountSpec(spec)
		if err != nil {
			t.Fatalf("ParseMountSpec(%q): %v", spec, err)
		}
		if m.ReadOnly != want {
			t.Errorf("ParseMountSpec(%q).ReadOnly = %v, want %v", spec, m.ReadOnly, want)
		}
	}
}
