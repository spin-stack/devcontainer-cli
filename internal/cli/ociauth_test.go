package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

func TestOCIAuthGlobalFlagValidation(t *testing.T) {
	startFakeEngine(t, nil)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "allow list requires hardening",
			args: []string{"--allow-cross-origin-auth-host", "registry.example=auth.example"},
			want: "--allow-cross-origin-auth-host requires --oci-auth-hardening.",
		},
		{
			name: "entry must be a pair",
			args: []string{"--oci-auth-hardening", "--allow-cross-origin-auth-host", "bad"},
			want: "Invalid cross-origin auth host 'bad'. Expected '<registry-host>=<auth-host>'.",
		},
		{
			name: "entry must hold exactly one separator",
			args: []string{"--oci-auth-hardening", "--allow-cross-origin-auth-host", "a=b=c"},
			want: "Invalid cross-origin auth host 'a=b=c'. Expected '<registry-host>=<auth-host>'.",
		},
		{
			name: "entry must be a bare authority",
			args: []string{"--oci-auth-hardening", "--allow-cross-origin-auth-host", "a/b=c"},
			want: "Invalid authority 'a/b'.",
		},
		{
			name: "valid mapping is accepted",
			args: []string{"--oci-auth-hardening", "--allow-cross-origin-auth-host", "registry.example=auth.example"},
		},
		{
			name: "hardening alone is accepted",
			args: []string{"--oci-auth-hardening"},
		},
		{
			name: "repeated mappings are accepted",
			args: []string{"--oci-auth-hardening",
				"--allow-cross-origin-auth-host", "registry.example=auth.example",
				"--allow-cross-origin-auth-host", "registry.example=other.example"},
		},
	}

	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".devcontainer"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".devcontainer", "devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand()
			args := append(append([]string{}, tt.args...), "read-configuration", "--workspace-folder", ws)
			root.SetArgs(args)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			err := root.Execute()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("flags should be accepted, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, command succeeded", tt.want)
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestExecAcceptsGlobalOCIAuthFlags(t *testing.T) {
	flags, cmd := splitExecArgs([]string{
		"--oci-auth-hardening",
		"--allow-cross-origin-auth-host", "registry.example=auth.example",
		"--workspace-folder", "/ws",
		"echo", "hello",
	})
	wantFlags := []string{"--oci-auth-hardening", "--allow-cross-origin-auth-host", "registry.example=auth.example", "--workspace-folder", "/ws"}
	if strings.Join(flags, " ") != strings.Join(wantFlags, " ") {
		t.Errorf("flags = %v, want %v", flags, wantFlags)
	}
	if strings.Join(cmd, " ") != "echo hello" {
		t.Errorf("command = %v, want [echo hello]", cmd)
	}

	root := NewRootCommand()
	root.SetArgs([]string{"exec", "--allow-cross-origin-auth-host", "registry.example=auth.example", "echo", "hi"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	if err == nil || err.Error() != "--allow-cross-origin-auth-host requires --oci-auth-hardening." {
		t.Errorf("exec error = %v, want the hardening requirement error", err)
	}
}

func TestReadConfigurationEmitsOCIAuthDiagnostics(t *testing.T) {
	startFakeEngine(t, nil)
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".devcontainer"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".devcontainer", "devcontainer.json"), []byte(`{"image":"ubuntu"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetArgs([]string{"read-configuration", "--workspace-folder", ws})
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("read-configuration: %v", err)
	}

	var parsed struct {
		OCIAuthDiagnostics *oci.AuthDiagnostics `json:"ociAuthDiagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("parse output %q: %v", stdout.String(), err)
	}
	if parsed.OCIAuthDiagnostics == nil {
		t.Fatalf("ociAuthDiagnostics missing from output: %s", stdout.String())
	}
	if *parsed.OCIAuthDiagnostics != (oci.AuthDiagnostics{}) {
		t.Errorf("no registry was contacted, want all-false diagnostics, got %+v", *parsed.OCIAuthDiagnostics)
	}
}

func TestOCIAuthPolicyFlowsFromCommandContext(t *testing.T) {
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			if !ociAuthPolicy(c.Context()).Hardening() {
				t.Error("--oci-auth-hardening did not reach the command context")
			}
			return nil
		},
	}
	root := NewRootCommand()
	root.AddCommand(probe)
	root.SetArgs([]string{"--oci-auth-hardening", "probe"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("probe: %v", err)
	}

	plain := NewRootCommand()
	plain.AddCommand(&cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			if ociAuthPolicy(c.Context()).Hardening() {
				t.Error("hardening must default to off")
			}
			return nil
		},
	})
	plain.SetArgs([]string{"probe"})
	plain.SetOut(&bytes.Buffer{})
	plain.SetErr(&bytes.Buffer{})
	if err := plain.Execute(); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

func TestOCIAuthGlobalArgsForwarding(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"no flags", []string{"probe"}, nil},
		{"hardening only", []string{"--oci-auth-hardening", "probe"}, []string{"--oci-auth-hardening"}},
		{
			"hardening with repeated mappings",
			[]string{"--oci-auth-hardening",
				"--allow-cross-origin-auth-host", "registry.example=auth.example",
				"--allow-cross-origin-auth-host", "other.example=auth.other", "probe"},
			[]string{"--oci-auth-hardening",
				"--allow-cross-origin-auth-host", "registry.example=auth.example",
				"--allow-cross-origin-auth-host", "other.example=auth.other"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			root := NewRootCommand()
			root.AddCommand(&cobra.Command{
				Use: "probe",
				RunE: func(c *cobra.Command, _ []string) error {
					got = ociAuthGlobalArgs(c)
					return nil
				},
			})
			root.SetArgs(tt.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			if err := root.Execute(); err != nil {
				t.Fatalf("probe: %v", err)
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("args = %v, want %v", got, tt.want)
			}
		})
	}
}
