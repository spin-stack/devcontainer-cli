package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
)

func writeSecretsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSecretValuesFromFile(t *testing.T) {
	s, err := loadSecretsFile(writeSecretsFile(t, `{"TOKEN":"s3cr=t","USER":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.maskValues()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"bob", "s3cr=t"}; !slices.Equal(got, want) {
		t.Errorf("values = %q, want %q", got, want)
	}

	s, err = loadSecretsFile("")
	if err != nil || s != nil {
		t.Errorf("no --secrets-file = %q, %v; want no secrets and no error", s, err)
	}
}

func TestLoadSecretsFileFailsAsTS(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	for _, tc := range []struct {
		name, path, message string
	}{
		{"missing file", missing, "ENOENT: no such file or directory, open '" + missing + "'"},
		{"relative missing file", "missing-secrets.json", "ENOENT: no such file or directory, open 'missing-secrets.json'"},
		{"directory", dir, "EISDIR: illegal operation on a directory, read"},
		{"not json", writeSecretsFile(t, `not json`), "Invalid json data"},
		{"empty", writeSecretsFile(t, ``), "Invalid json data"},
		{"trailing comma", writeSecretsFile(t, `{"A":"1",}`), "Invalid json data"},
		{"array trailing comma", writeSecretsFile(t, `[1,2,]`), "Invalid json data"},
		{"one-element array trailing comma", writeSecretsFile(t, `{"A":["x",]}`), "Invalid json data"},
		{"one-member object trailing comma", writeSecretsFile(t, `{"A":{"x":1,}}`), "Invalid json data"},
		{"trailing data", writeSecretsFile(t, `{"A":"1"} x`), "Invalid json data"},
		{"bom", writeSecretsFile(t, "\xEF\xBB\xBF{\"A\":\"1\"}"), "Invalid json data"},
		{"unquoted name", writeSecretsFile(t, `{A:"1"}`), "Invalid json data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := loadSecretsFile(tc.path)
			if s != nil {
				t.Errorf("secrets = %q, want none", s)
			}
			ce, ok := coreerrors.AsContainerError(err)
			if !ok {
				t.Fatalf("error = %v (%T), want a ContainerError", err, err)
			}
			got := coreerrors.ToErrorOutput(ce)
			want := coreerrors.ErrorOutput{Outcome: "error", Message: tc.message, Description: "Failed to read/parse secrets"}
			if got != want {
				t.Errorf("error output = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLoadSecretsFileAcceptsWhatJSONCParses(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		env, mask     []string
	}{
		{"comments", "{\n// line\n\"A\":\"x\" /* block */\n}", []string{"A=x"}, []string{"x"}},
		{"non-string values", `{"N":1,"F":1.5,"E":1e21,"T":true,"O":{"x":1},"L":[1,null,"b"],"Z":[]}`,
			[]string{"N=1", "F=1.5", "E=1e+21", "T=true", "O=[object Object]", "L=1,,b", "Z="}, []string{"1,,b"}},
		{"array", `[1,"two"]`, []string{"0=1", "1=two"}, []string{"two"}},
		{"string", `"ab"`, []string{"0=a", "1=b"}, []string{"a", "b"}},
		{"null", `null`, []string{}, nil},
		{"number", `5`, []string{}, nil},
		{"repeated and index keys", `{"B":"1","2":"x","A":"2","B":"3","1":"y"}`,
			[]string{"1=y", "2=x", "B=3", "A=2"}, []string{"y", "x", "3", "2"}},
		// Expected values below are what node prints for the reference's
		// jsonc.parse followed by `{...secrets}`.
		{"array index boundaries",
			`{"B":"b","4294967295":"max","4294967294":"top","01":"lead","0":"zero","-1":"neg","99999999999999999999":"long","10":"ten","2":"two"}`,
			[]string{"0=zero", "2=two", "10=ten", "4294967294=top", "B=b", "4294967295=max", "01=lead", "-1=neg", "99999999999999999999=long"},
			[]string{"zero", "two", "ten", "top", "b", "max", "lead", "neg", "long"}},
		{"String of numbers",
			`{"Z":-0,"M":-0.0e5,"BIG":1.7976931348623157e308,"NBIG":-1.7976931348623157e308,"I":1e400,"NI":-1e400,"E20":1e20,"E21":1e21,"L":123456789012345678901234,"S":1e-7,"T":0.000001}`,
			[]string{"Z=0", "M=0", "BIG=1.7976931348623157e+308", "NBIG=-1.7976931348623157e+308", "I=Infinity", "NI=-Infinity",
				"E20=100000000000000000000", "E21=1e+21", "L=1.2345678901234569e+23", "S=1e-7", "T=0.000001"}, nil},
		{"masking by length", `{"A":["x"],"N":[null],"NN":[[null,1],null],"E":[],"S":""}`,
			[]string{"A=x", "N=", "NN=,1,", "E=", "S="}, []string{"x", "", ",1,"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := loadSecretsFile(writeSecretsFile(t, tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if got := s.env(); !slices.Equal(got, tc.env) {
				t.Errorf("env = %q, want %q", got, tc.env)
			}
			mask, err := s.maskValues()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(mask, tc.mask) {
				t.Errorf("mask values = %q, want %q", mask, tc.mask)
			}
		})
	}
}

func TestSecretsMaskValuesFailsOnNullAsTS(t *testing.T) {
	s, err := loadSecretsFile(writeSecretsFile(t, `{"A":"x","B":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.env(); !slices.Equal(got, []string{"A=x", "B=null"}) {
		t.Errorf("env = %q", got)
	}
	if _, err := s.maskValues(); err == nil || err.Error() != "Cannot read properties of null (reading 'length')" {
		t.Errorf("mask error = %v, want the TS TypeError message", err)
	}
}
