package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSELinux(t *testing.T) {
	write := func(t *testing.T, content string) string {
		p := filepath.Join(t.TempDir(), "enforce")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	tests := []struct {
		name       string
		path       string
		wantStatus Status
	}{
		{"enforcing warns", write(t, "1\n"), StatusWarn},
		{"permissive is ok", write(t, "0\n"), StatusOK},
		{"absent (not enabled) is ok", "/nonexistent/selinux/enforce", StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkSELinux(t.Context(), &Env{SELinuxEnforcePath: tt.path})
			if r.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s (%s)", r.Status, tt.wantStatus, r.Summary)
			}
			if tt.wantStatus == StatusWarn && r.Remediation == "" {
				t.Error("enforcing SELinux warn must carry a remediation")
			}
		})
	}
}
