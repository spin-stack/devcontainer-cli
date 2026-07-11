package lifecycle

import (
	"testing"
)

func TestParseEnvOutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		want        map[string]string // exact key/value assertions
		wantPresent []string          // keys that must exist (value not checked)
		wantLen     int               // -1 to skip the total-length assertion
	}{
		{
			name: "simple",
			output: `HOME=/home/user
PATH=/usr/bin:/usr/local/bin
SHELL=/bin/bash
USER=vscode`,
			want: map[string]string{
				"HOME":  "/home/user",
				"PATH":  "/usr/bin:/usr/local/bin",
				"SHELL": "/bin/bash",
				"USER":  "vscode",
			},
			wantLen: -1,
		},
		{
			name: "multi-line value",
			output: `SIMPLE=value
BASH_FUNC_module%%=() { eval $(/usr/bin/modulecmd bash $*)
}
ANOTHER=other`,
			want: map[string]string{
				"SIMPLE":  "value",
				"ANOTHER": "other",
			},
			// BASH_FUNC should capture the multi-line value
			wantPresent: []string{"BASH_FUNC_module%%"},
			wantLen:     -1,
		},
		{
			name:    "empty",
			output:  "",
			wantLen: 0,
		},
		{
			name:   "value with equals",
			output: `CONN_STRING=host=localhost;port=5432;db=mydb`,
			want: map[string]string{
				"CONN_STRING": "host=localhost;port=5432;db=mydb",
			},
			wantLen: -1,
		},
		{
			name: "empty value",
			output: `EMPTY=
NEXT=value`,
			want: map[string]string{
				"EMPTY": "",
				"NEXT":  "value",
			},
			wantLen: -1,
		},
		{
			name:   "carriage return",
			output: "KEY=value\r\nOTHER=stuff\r\n",
			want: map[string]string{
				"KEY":   "value",
				"OTHER": "stuff",
			},
			wantLen: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := parseEnvOutput(tt.output)
			for k, v := range tt.want {
				if env[k] != v {
					t.Errorf("%s = %q, want %q", k, env[k], v)
				}
			}
			for _, k := range tt.wantPresent {
				if _, ok := env[k]; !ok {
					t.Errorf("missing %s", k)
				}
			}
			if tt.wantLen >= 0 && len(env) != tt.wantLen {
				t.Errorf("expected %d entries, got %d", tt.wantLen, len(env))
			}
		})
	}
}

func TestMergePaths(t *testing.T) {
	// Base entries missing from the shell PATH are inserted in relative order.
	got := mergePaths("/opt/bin:/usr/bin", "/usr/local/bin:/usr/bin:/bin", true)
	if got != "/usr/local/bin:/opt/bin:/usr/bin:/bin" {
		t.Errorf("got %q", got)
	}
	// Non-root skips /sbin dirs.
	got = mergePaths("/usr/bin", "/usr/local/sbin:/usr/bin:/sbin", false)
	if got != "/usr/bin" {
		t.Errorf("non-root sbin skip: got %q", got)
	}
	// Root keeps /sbin dirs.
	got = mergePaths("/usr/bin", "/usr/bin:/sbin", true)
	if got != "/usr/bin:/sbin" {
		t.Errorf("root sbin: got %q", got)
	}
}
