package config

import "testing"

// TestComputeDevContainerID_TSParity pins Go's ${devcontainerId} algorithm to the
// TS oracle (v0.88.0, spec-common/variableSubstitution.ts devcontainerIdForLabels:
// JSON.stringify(labels, sortedKeys) → sha256 → BigInt.toString(32).padStart(52,'0')).
// The expected values were computed with the reference implementation. A cross-
// command substitution divergence here would silently break every ${devcontainerId}
// use in up/build/exec — and the parity matrix can't catch it because each side runs
// with different --id-labels (so the resolved IDs legitimately differ per side).
func TestComputeDevContainerID_TSParity(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "two labels",
			labels: map[string]string{"foo": "bar", "parity.test": "devid"},
			want:   "0eqk35tddobe0171rebd7mvh8npbq2f49ctp543uiimslnjgumv5",
		},
		{
			name:   "devcontainer local_folder + config_file",
			labels: map[string]string{"devcontainer.local_folder": "/w", "devcontainer.config_file": "/w/.devcontainer.json"},
			want:   "0c27kmi3t5h3anj4psh9usfultojkmt45hfb3mf98sm13ug01diu",
		},
		{
			name:   "single label",
			labels: map[string]string{"a": "1"},
			want:   "16num3pb40viagoiti6uqh0t0ccbfgqcavs6inmu8b922mhgo2b0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeDevContainerID(tc.labels)
			if got != tc.want {
				t.Errorf("ComputeDevContainerID(%v)\n  got:  %s\n  want: %s (TS oracle)", tc.labels, got, tc.want)
			}
			if len(got) != 52 {
				t.Errorf("devcontainerId length = %d, want 52 (padStart)", len(got))
			}
		})
	}
}
