package imagemeta

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeConfigurationMemoryAsTSParseBytesReadsIt(t *testing.T) {
	tests := []struct {
		memory, want string
	}{
		{"1024", `{"cpus":0,"memory":"1024"}`},
		{"1tb", `{"cpus":0,"memory":"1099511627776"}`},
		{"3mb", `{"cpus":0,"memory":"3145728"}`},
		{"5kb", `{"cpus":0,"memory":"5120"}`},
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

func mustEntries(t *testing.T, jsonArray string) []Entry {
	t.Helper()
	var entries []Entry
	if err := json.Unmarshal([]byte(jsonArray), &entries); err != nil {
		t.Fatalf("bad entries JSON: %v", err)
	}
	return entries
}
