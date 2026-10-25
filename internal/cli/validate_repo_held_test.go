package cli

import "testing"

// An --id-label is <name>=<value>: a name and a value on both sides of an '='.
// TS rejects a label without '=', with an empty name or with an empty value.
func TestValidateIDLabels(t *testing.T) {
	for _, ok := range []string{"a=b", "devcontainer.local_folder=/x", "a=b=c"} {
		if err := validateIDLabels([]string{ok}); err != nil {
			t.Errorf("validateIDLabels(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"ab", "=b", "a=", "="} {
		err := validateIDLabels([]string{"ok=1", bad})
		if err == nil || err.Error() != "Unmatched argument format: id-label must match <name>=<value>" {
			t.Errorf("validateIDLabels(%q) = %v, want the format error", bad, err)
		}
	}
	if err := validateIDLabels(nil); err != nil {
		t.Errorf("no labels: %v", err)
	}
}

// A --remote-env needs a name before its '='; an empty value is allowed.
func TestValidateRemoteEnvs(t *testing.T) {
	for _, ok := range []string{"A=b", "A=", "A=b=c"} {
		if err := validateRemoteEnvs([]string{ok}); err != nil {
			t.Errorf("validateRemoteEnvs(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"A", "=b"} {
		err := validateRemoteEnvs([]string{"OK=1", bad})
		if err == nil || err.Error() != "Unmatched argument format: remote-env must match <name>=<value>" {
			t.Errorf("validateRemoteEnvs(%q) = %v, want the format error", bad, err)
		}
	}
}

func TestValidateMounts(t *testing.T) {
	for _, ok := range []string{
		"type=bind,source=/a,target=/b",
		"type=volume,source=v,target=/b,external=true",
	} {
		if err := validateMounts([]string{ok}); err != nil {
			t.Errorf("validateMounts(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"type=tmpfs,source=a,target=/b", "source=/a,target=/b", "type=bind,source=/a,target=/b,external=yes"} {
		if err := validateMounts([]string{bad}); err == nil {
			t.Errorf("validateMounts(%q) = nil, want an error", bad)
		}
	}
}

// --terminal-columns and --terminal-rows imply each other, as yargs .implies().
func TestValidateTerminalImplications(t *testing.T) {
	cases := []struct {
		cols, rows int
		want       string
	}{
		{0, 0, ""},
		{80, 24, ""},
		{80, 0, "Implications failed:\n terminal-columns -> terminal-rows"},
		{0, 24, "Implications failed:\n terminal-rows -> terminal-columns"},
	}
	for _, c := range cases {
		err := validateTerminalImplications(c.cols, c.rows)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("validateTerminalImplications(%d, %d) = %q, want %q", c.cols, c.rows, got, c.want)
		}
	}
}

// The first flag whose value is not one of its choices is the one reported.
func TestValidateEnums(t *testing.T) {
	if err := validateEnums(enumFlag{"log-level", "debug", logLevels}, enumFlag{"log-format", "json", logFormats}); err != nil {
		t.Errorf("valid choices: %v", err)
	}
	err := validateEnums(enumFlag{"log-level", "loud", logLevels}, enumFlag{"log-format", "xml", logFormats})
	if want := `Invalid value "loud" for --log-level. Choose from: info, debug, trace`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}
