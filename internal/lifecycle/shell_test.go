package lifecycle

import (
	"bufio"
	"strings"
	"testing"
)

func TestEOTConstant(t *testing.T) {
	if EOT != "\u2404" {
		t.Errorf("EOT = %q, want \\u2404", EOT)
	}
	b := []byte(EOT)
	if len(b) != 3 || b[0] != 0xe2 || b[1] != 0x90 || b[2] != 0x84 {
		t.Errorf("EOT bytes = %x, want [e2 90 84]", b)
	}
}

func TestReadSegment(t *testing.T) {
	input := "hello world" + EOT
	br := bufio.NewReader(strings.NewReader(input))

	segment, err := readSegment(br)
	if err != nil {
		t.Fatal(err)
	}
	if segment != "hello world" {
		t.Errorf("segment = %q", segment)
	}
}

func TestReadSegment_Empty(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(EOT))

	segment, err := readSegment(br)
	if err != nil {
		t.Fatal(err)
	}
	if segment != "" {
		t.Errorf("segment = %q, want empty", segment)
	}
}

func TestReadUntilEOT_FullProtocol(t *testing.T) {
	input := EOT + "command output" + EOT + "0" + EOT
	br := bufio.NewReader(strings.NewReader(input))

	stdout, code, err := readUntilEOT(br)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "command output" {
		t.Errorf("stdout = %q", stdout)
	}
	if code != "0" {
		t.Errorf("code = %q", code)
	}
}

func TestReadUntilEOT_NonZeroExit(t *testing.T) {
	input := EOT + "error output" + EOT + "1" + EOT
	br := bufio.NewReader(strings.NewReader(input))

	stdout, code, err := readUntilEOT(br)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "error output" {
		t.Errorf("stdout = %q", stdout)
	}
	if code != "1" {
		t.Errorf("code = %q", code)
	}
}

func TestReadUntilEOT_EmptyOutput(t *testing.T) {
	input := EOT + "" + EOT + "0" + EOT
	br := bufio.NewReader(strings.NewReader(input))

	stdout, code, err := readUntilEOT(br)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if code != "0" {
		t.Errorf("code = %q", code)
	}
}

func TestReadUntilEOT_MultilineOutput(t *testing.T) {
	input := EOT + "line1\nline2\nline3" + EOT + "0" + EOT
	br := bufio.NewReader(strings.NewReader(input))

	stdout, _, err := readUntilEOT(br)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "line1\nline2\nline3" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "''",
		},
		{
			name:  "plain",
			input: "/workspaces/project",
			want:  "'/workspaces/project'",
		},
		{
			name:  "embedded single quote",
			input: "/tmp/it's-here",
			want:  "'/tmp/it'\"'\"'s-here'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellSingleQuote(tt.input); got != tt.want {
				t.Fatalf("shellSingleQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
