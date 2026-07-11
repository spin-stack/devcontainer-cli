package lifecycle

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
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

func TestReadUntilEOT(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantStdout string
		wantCode   string
	}{
		{
			name:       "full protocol",
			input:      EOT + "command output" + EOT + "0" + EOT,
			wantStdout: "command output",
			wantCode:   "0",
		},
		{
			name:       "non-zero exit",
			input:      EOT + "error output" + EOT + "1" + EOT,
			wantStdout: "error output",
			wantCode:   "1",
		},
		{
			name:       "empty output",
			input:      EOT + "" + EOT + "0" + EOT,
			wantStdout: "",
			wantCode:   "0",
		},
		{
			name:       "multiline output",
			input:      EOT + "line1\nline2\nline3" + EOT + "0" + EOT,
			wantStdout: "line1\nline2\nline3",
			wantCode:   "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(tt.input))

			stdout, code, err := readUntilEOT(br)
			if err != nil {
				t.Fatal(err)
			}
			if stdout != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestReadSegment_EOFBeforeEOT(t *testing.T) {
	// A stream that ends without an EOT returns the partial text plus the error,
	// so a truncated/aborted shell response is surfaced rather than hanging.
	br := bufio.NewReader(strings.NewReader("partial output no marker"))
	segment, err := readSegment(br)
	if err == nil {
		t.Fatal("expected EOF error on unterminated segment")
	}
	if segment != "partial output no marker" {
		t.Errorf("segment = %q, want the partial text", segment)
	}
}

func TestReadSegment_MultibyteNonEOT(t *testing.T) {
	// A 3-byte UTF-8 rune that starts with 0xe2 but is NOT EOT (U+2192 '→',
	// bytes e2 86 92) must be preserved verbatim, not mistaken for a marker.
	input := "a→b" + EOT
	br := bufio.NewReader(strings.NewReader(input))
	segment, err := readSegment(br)
	if err != nil {
		t.Fatal(err)
	}
	if segment != "a→b" {
		t.Errorf("segment = %q, want %q", segment, "a→b")
	}
}

func TestReadUntilEOT_Truncated(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no start marker", "no eot at all"},
		{"start only", EOT + "stdout but no closing markers"},
		{"missing exit code segment", EOT + "out" + EOT + "code-never-terminated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(tt.input))
			if _, _, err := readUntilEOT(br); err == nil {
				t.Errorf("expected error for truncated stream %q", tt.input)
			}
		})
	}
}

// TestShellServer_ExecProtocol drives Exec against an in-memory pipe playing the
// container side of the EOT protocol: it echoes the wrapper's EOT framing plus a
// synthetic exit code, so the read loop is exercised with no real Docker.
func TestShellServer_ExecProtocol(t *testing.T) {
	inR, inW := io.Pipe()   // shell server writes commands here
	outR, outW := io.Pipe() // shell server reads responses here
	errR, errW := io.Pipe() // shell server reads stderr (EOT-terminated) here

	s := &ShellServer{
		stdin:  inW,
		stdout: bufio.NewReader(outR),
		stderr: bufio.NewReader(errR),
		log:    log.Null,
	}

	// Fake container: consume each command line and reply with framed stdout,
	// then close the per-command stderr segment with its EOT — mirroring the
	// wrapper's `echo -n {EOT} >&2`.
	go func() {
		br := bufio.NewReader(inR)
		replies := []string{
			EOT + "hello\n" + EOT + "0" + EOT,
			EOT + "" + EOT + "42" + EOT,
		}
		for _, reply := range replies {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
			if _, err := io.WriteString(outW, reply); err != nil {
				return
			}
			if _, err := io.WriteString(errW, EOT); err != nil {
				return
			}
		}
	}()

	stdout, code, err := s.Exec("echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "hello\n" || code != 0 {
		t.Errorf("first Exec = (%q, %d), want (\"hello\\n\", 0)", stdout, code)
	}

	// A second, serialized command reads the non-zero exit code correctly.
	stdout, code, err = s.Exec("false")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || code != 42 {
		t.Errorf("second Exec = (%q, %d), want (\"\", 42)", stdout, code)
	}
}

func TestShellServer_ExecWriteError(t *testing.T) {
	// A closed stdin makes the command write fail: Exec must report it, not hang.
	_, inW := io.Pipe()
	inW.Close()
	s := &ShellServer{stdin: inW, log: log.Null}
	if _, _, err := s.Exec("echo hi"); err == nil {
		t.Fatal("expected write error on closed stdin")
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
