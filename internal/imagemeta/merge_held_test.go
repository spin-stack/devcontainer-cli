package imagemeta

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMergeConfigurationMemoryAsTSParseBytesReadsIt holds the TS parseBytes
// grammar (reference/src/spec-node/imageMetadata.ts), /^(\d+)([tgmk]b)?$/
// times 2**40, 2**30, 2**20, 2**10 or 1, through the merged hostRequirements
// memory, `${memory}` or left out when 0. The count is a JS number: past the
// int64 range it keeps growing and `${n}` switches to exponent notation from
// 1e21 on. The wants are what node prints for the same input.
func TestMergeConfigurationMemoryAsTSParseBytesReadsIt(t *testing.T) {
	tests := []struct {
		memory, want string
	}{
		{"1024", `{"cpus":0,"memory":"1024"}`},
		{"1tb", `{"cpus":0,"memory":"1099511627776"}`},
		{"3mb", `{"cpus":0,"memory":"3145728"}`},
		{"5kb", `{"cpus":0,"memory":"5120"}`},
		// The regex has no sign, no other suffix and no bare suffix: 0, and a
		// requirement of 0 is no requirement.
		{"+5gb", `null`},
		{"-5", `null`},
		{"5b", `null`},
		{"kb", `null`},
		{"b", `null`},
		{"99999999999999999999", `{"cpus":0,"memory":"100000000000000000000"}`},
		{"1000000000000000000000", `{"cpus":0,"memory":"1e+21"}`},
		{"10000000000tb", `{"cpus":0,"memory":"1.099511627776e+22"}`},
		{"17976931348623157" + strings.Repeat("0", 292), `{"cpus":0,"memory":"1.7976931348623157e+308"}`},
		{strings.Repeat("1", 400), `{"cpus":0,"memory":"Infinity"}`},
	}
	for _, tt := range tests {
		t.Run(tt.memory, func(t *testing.T) {
			entries := []Entry{{HostRequirements: map[string]any{"memory": tt.memory}}}
			data, err := json.Marshal(MergeConfiguration(entries).HostRequirements)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("hostRequirements = %s, want %s", data, tt.want)
			}
		})
	}
}

// TestMergeConfigurationTurnsOnlyLocalhostDigitsIntoPortNumbers: TS
// mergeForwardPorts (reference/src/spec-node/imageMetadata.ts) keys a number N
// as "localhost:N" and parses back only what /localhost:\d+/ matches, so a
// numeric string and a signed port stay the strings they are.
func TestMergeConfigurationTurnsOnlyLocalhostDigitsIntoPortNumbers(t *testing.T) {
	m := MergeConfiguration(mustEntries(t, `[{"forwardPorts": ["3000", "localhost:+12", "localhost:8080", 3000]}]`))
	data, err := json.Marshal(m.ForwardPorts)
	if err != nil {
		t.Fatal(err)
	}
	if want := `["3000","localhost:+12",8080,3000]`; string(data) != want {
		t.Errorf("forwardPorts = %s, want %s", data, want)
	}
}
