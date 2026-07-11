package lifecycle

import (
	"testing"
)

func TestParseEnvOutput_Simple(t *testing.T) {
	output := `HOME=/home/user
PATH=/usr/bin:/usr/local/bin
SHELL=/bin/bash
USER=vscode`

	env := parseEnvOutput(output)

	if env["HOME"] != "/home/user" {
		t.Errorf("HOME = %q", env["HOME"])
	}
	if env["PATH"] != "/usr/bin:/usr/local/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if env["SHELL"] != "/bin/bash" {
		t.Errorf("SHELL = %q", env["SHELL"])
	}
	if env["USER"] != "vscode" {
		t.Errorf("USER = %q", env["USER"])
	}
}

func TestParseEnvOutput_MultiLineValue(t *testing.T) {
	output := `SIMPLE=value
BASH_FUNC_module%%=() { eval $(/usr/bin/modulecmd bash $*)
}
ANOTHER=other`

	env := parseEnvOutput(output)

	if env["SIMPLE"] != "value" {
		t.Errorf("SIMPLE = %q", env["SIMPLE"])
	}
	if env["ANOTHER"] != "other" {
		t.Errorf("ANOTHER = %q", env["ANOTHER"])
	}
	// BASH_FUNC should capture the multi-line value
	if _, ok := env["BASH_FUNC_module%%"]; !ok {
		t.Error("missing BASH_FUNC_module%%")
	}
}

func TestParseEnvOutput_Empty(t *testing.T) {
	env := parseEnvOutput("")
	if len(env) != 0 {
		t.Errorf("expected empty, got %d entries", len(env))
	}
}

func TestParseEnvOutput_ValueWithEquals(t *testing.T) {
	output := `CONN_STRING=host=localhost;port=5432;db=mydb`

	env := parseEnvOutput(output)
	if env["CONN_STRING"] != "host=localhost;port=5432;db=mydb" {
		t.Errorf("CONN_STRING = %q", env["CONN_STRING"])
	}
}

func TestParseEnvOutput_EmptyValue(t *testing.T) {
	output := `EMPTY=
NEXT=value`

	env := parseEnvOutput(output)
	if env["EMPTY"] != "" {
		t.Errorf("EMPTY = %q", env["EMPTY"])
	}
	if env["NEXT"] != "value" {
		t.Errorf("NEXT = %q", env["NEXT"])
	}
}

func TestParseEnvOutput_CarriageReturn(t *testing.T) {
	output := "KEY=value\r\nOTHER=stuff\r\n"

	env := parseEnvOutput(output)
	if env["KEY"] != "value" {
		t.Errorf("KEY = %q", env["KEY"])
	}
	if env["OTHER"] != "stuff" {
		t.Errorf("OTHER = %q", env["OTHER"])
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
