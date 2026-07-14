package devtools

import (
	"strings"
	"testing"
)

const sampleProfile = `mode: set
github.com/devcontainers/cli/internal/cli/a.go:1.1,3.2 5 1
github.com/devcontainers/cli/internal/cli/a.go:4.1,5.2 3 0
github.com/devcontainers/cli/internal/docker/b.go:1.1,2.2 4 1
github.com/devcontainers/cli/internal/docker/b.go:3.1,4.2 2 1
`

func parseSample(t *testing.T) ([]PkgCov, PkgCov) {
	t.Helper()
	pkgs, total, err := ParseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	return pkgs, total
}

// TestParseProfile checks statement-weighted aggregation (covered iff count>0),
// matching the former pkgcov.awk.
func TestParseProfile(t *testing.T) {
	pkgs, total := parseSample(t)
	want := []PkgCov{
		{"github.com/devcontainers/cli/internal/cli", 5, 8},
		{"github.com/devcontainers/cli/internal/docker", 6, 6},
	}
	if len(pkgs) != len(want) {
		t.Fatalf("got %d packages, want %d: %+v", len(pkgs), len(want), pkgs)
	}
	for i, p := range pkgs {
		if p != want[i] {
			t.Errorf("pkg[%d] = %+v, want %+v", i, p, want[i])
		}
	}
	if total != (PkgCov{"TOTAL", 11, 14}) {
		t.Errorf("total = %+v, want {TOTAL 11 14}", total)
	}
	if got := total.Pct(); got != 78.6 {
		t.Errorf("total Pct = %v, want 78.6", got)
	}
	if got := pkgs[0].Pct(); got != 62.5 {
		t.Errorf("cli Pct = %v, want 62.5", got)
	}
}

// TestRenderReport is a golden check against coverage-report.sh output.
func TestRenderReport(t *testing.T) {
	pkgs, total := parseSample(t)
	want := "### Coverage — sample (total 78.6%)\n\n" +
		"| Package | Stmts | Coverage |\n|---|---:|---:|\n" +
		"| internal/cli | 8 | 62.5% |\n" +
		"| internal/docker | 6 | 100.0% |\n" +
		"| **TOTAL** | **14** | **78.6%** |\n"
	if got := RenderReport(pkgs, total, "sample"); got != want {
		t.Errorf("RenderReport mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReportNoData(t *testing.T) {
	want := "### Coverage — empty\n\n_(no coverage data)_\n"
	if got := RenderReport(nil, PkgCov{Pkg: "TOTAL"}, "empty"); got != want {
		t.Errorf("RenderReport(empty) = %q, want %q", got, want)
	}
}

// TestCoverageGate mirrors coverage-gate.sh: pass/fail lines and exit signal.
func TestCoverageGate(t *testing.T) {
	pkgs, total := parseSample(t)

	t.Run("all pass", func(t *testing.T) {
		floors, _ := ParseFloors([]string{"TOTAL=70", "cli=60", "docker=90"})
		res, ok := CoverageGate(pkgs, total, floors)
		if !ok {
			t.Errorf("expected pass, got ok=false")
		}
		wantLines := []string{
			"OK:   TOTAL coverage 78.6% >= 70% floor",
			"OK:   cli coverage 62.5% >= 60% floor",
			"OK:   docker coverage 100.0% >= 90% floor",
		}
		for i, w := range wantLines {
			if res[i].Line != w {
				t.Errorf("line[%d] = %q, want %q", i, res[i].Line, w)
			}
		}
	})

	t.Run("below floor fails", func(t *testing.T) {
		floors, _ := ParseFloors([]string{"TOTAL=70", "cli=80"})
		res, ok := CoverageGate(pkgs, total, floors)
		if ok {
			t.Errorf("expected fail, got ok=true")
		}
		if res[1].Line != "FAIL: cli coverage 62.5% is below the 80% floor" {
			t.Errorf("got %q", res[1].Line)
		}
	})

	t.Run("missing package fails", func(t *testing.T) {
		floors, _ := ParseFloors([]string{"oci=50"})
		res, ok := CoverageGate(pkgs, total, floors)
		if ok || res[0].Line != "FAIL: no coverage data for oci" {
			t.Errorf("got ok=%v line=%q, want fail + no-data", ok, res[0].Line)
		}
	})
}

func TestParseFloorsInvalid(t *testing.T) {
	for _, bad := range []string{"cli", "=50", "cli=abc"} {
		if _, err := ParseFloors([]string{bad}); err == nil {
			t.Errorf("ParseFloors(%q) succeeded, want error", bad)
		}
	}
}
