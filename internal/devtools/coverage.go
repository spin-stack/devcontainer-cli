package devtools

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// modPrefix is this module's import prefix; report rows strip it, and gate keys
// are matched under <modPrefix>internal/<key>.
const modPrefix = "github.com/devcontainers/cli/"

// PkgCov is statement-weighted coverage for one package (or the synthetic TOTAL).
type PkgCov struct {
	Pkg     string // import path, or "TOTAL"
	Covered int    // covered statements
	Total   int    // total statements
}

// Pct is the statement-weighted percentage, rounded to one decimal to match
// `go tool cover` / the former pkgcov.awk (the gate compares this rounded value).
func (p PkgCov) Pct() float64 {
	if p.Total == 0 {
		return 0
	}
	// Round to 1 decimal exactly as fmt "%.1f" would, so String() and numeric
	// comparisons never disagree at a boundary.
	v, _ := strconv.ParseFloat(fmt.Sprintf("%.1f", float64(p.Covered)*100/float64(p.Total)), 64)
	return v
}

// ParseProfile aggregates a Go cover profile (text format) into weighted
// per-package coverage plus a TOTAL. Statement-weighted, not a mean of per-file
// ratios — matching `go tool cover`. Ported from pkgcov.awk. Packages are returned
// sorted by import path; total is the module-wide row.
func ParseProfile(r io.Reader) (pkgs []PkgCov, total PkgCov, err error) {
	cov := map[string]int{}
	tot := map[string]int{}
	var gCov, gTot int

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if strings.HasPrefix(line, "mode:") {
				continue
			}
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// "import/path/file.go:1.1,3.2" -> "import/path" (package import path).
		p := fields[0]
		if i := strings.IndexByte(p, ':'); i >= 0 {
			p = p[:i]
		}
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[:i]
		}
		stmts, _ := strconv.Atoi(fields[1])
		count, _ := strconv.Atoi(fields[2])
		tot[p] += stmts
		gTot += stmts
		if count > 0 {
			cov[p] += stmts
			gCov += stmts
		}
	}
	if err := sc.Err(); err != nil {
		return nil, PkgCov{}, err
	}

	for p := range tot {
		pkgs = append(pkgs, PkgCov{Pkg: p, Covered: cov[p], Total: tot[p]})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Pkg < pkgs[j].Pkg })
	return pkgs, PkgCov{Pkg: "TOTAL", Covered: gCov, Total: gTot}, nil
}

// RenderReport renders a per-package coverage table plus the module total as
// GitHub-flavored Markdown. Returns the "no data" form when there are no packages.
// Ported from coverage-report.sh.
func RenderReport(pkgs []PkgCov, total PkgCov, title string) string {
	if len(pkgs) == 0 {
		return fmt.Sprintf("### Coverage — %s\n\n_(no coverage data)_\n", title)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### Coverage — %s (total %.1f%%)\n\n", title, total.Pct())
	b.WriteString("| Package | Stmts | Coverage |\n|---|---:|---:|\n")
	for _, p := range pkgs {
		name := strings.TrimPrefix(p.Pkg, modPrefix)
		fmt.Fprintf(&b, "| %s | %d | %.1f%% |\n", name, p.Total, p.Pct())
	}
	fmt.Fprintf(&b, "| **TOTAL** | **%d** | **%.1f%%** |\n", total.Total, total.Pct())
	return b.String()
}

// GateResult is one floor check; OK false means the floor was missed (or the
// package had no data).
type GateResult struct {
	Key  string
	Line string
	OK   bool
}

// CoverageGate enforces coverage floors: a floor for "TOTAL" checks the module
// total, any other key <k> checks the package at <modPrefix>internal/<k>. Returns
// one result per floor (in the given order) and ok=false if any failed. Ported
// from coverage-gate.sh.
func CoverageGate(pkgs []PkgCov, total PkgCov, floors []Floor) (results []GateResult, ok bool) {
	byPkg := map[string]PkgCov{total.Pkg: total}
	for _, p := range pkgs {
		byPkg[p.Pkg] = p
	}

	ok = true
	for _, fl := range floors {
		key := modPrefix + "internal/" + fl.Key
		if fl.Key == "TOTAL" {
			key = "TOTAL"
		}
		p, found := byPkg[key]
		if !found {
			results = append(results, GateResult{Key: fl.Key, OK: false,
				Line: fmt.Sprintf("FAIL: no coverage data for %s", fl.Key)})
			ok = false
			continue
		}
		got := p.Pct()
		if got < fl.Min {
			results = append(results, GateResult{Key: fl.Key, OK: false,
				Line: fmt.Sprintf("FAIL: %s coverage %.1f%% is below the %g%% floor", fl.Key, got, fl.Min)})
			ok = false
		} else {
			results = append(results, GateResult{Key: fl.Key, OK: true,
				Line: fmt.Sprintf("OK:   %s coverage %.1f%% >= %g%% floor", fl.Key, got, fl.Min)})
		}
	}
	return results, ok
}

// Floor is a "<key>=<min>" coverage threshold.
type Floor struct {
	Key string
	Min float64
}

// ParseFloors parses "TOTAL=48 cli=28 docker=77" style specs (already split into
// args). A malformed spec is an error so a typo in the Taskfile fails loudly.
func ParseFloors(specs []string) ([]Floor, error) {
	floors := make([]Floor, 0, len(specs))
	for _, s := range specs {
		k, v, found := strings.Cut(s, "=")
		if !found || k == "" {
			return nil, fmt.Errorf("invalid floor %q: want <key>=<min>", s)
		}
		min, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid floor %q: %w", s, err)
		}
		floors = append(floors, Floor{Key: k, Min: min})
	}
	return floors, nil
}
