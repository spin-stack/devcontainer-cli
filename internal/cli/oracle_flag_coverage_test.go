package cli

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOracleFlagCoverage guards the flag inventory against the ACTUAL TypeScript
// oracle, not a hand-maintained copy: for every top-level command the oracle
// defines, each of its flags must appear in docs/parity/cli-flags-inventory.yaml
// (which TestFlagInventoryParity in turn pins to the live Cobra tree). Together
// the two tests make oracle → inventory → Cobra tree a closed loop, so a flag the
// upstream CLI adds cannot silently go missing from `up`/`build`/… again.
//
// The oracle lives in the reference/ submodule, present only in the parity CI
// lane; the hermetic unit lane skips this test.
func TestOracleFlagCoverage(t *testing.T) {
	const oraclePath = "../../reference/src/spec-node/devContainersSpecCLI.ts"
	src, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Skipf("oracle not checked out (%s); run `task reference`", oraclePath)
	}

	// deliberatelyUnmodeled: oracle flags we intentionally do not expose, with the
	// reason. Keep this tiny and justified — a real new upstream flag must be added
	// to the CLI, not silenced here.
	deliberatelyUnmodeled := map[string]string{}

	oracle := oracleCommandFlags(string(src))
	inventory := inventoryCommandFlags(t)

	var problems []string
	for cmd, flags := range oracle {
		invFlags, ok := inventory[cmd]
		if !ok {
			continue // command not modeled in the inventory (e.g. features/* live elsewhere)
		}
		for f := range flags {
			if _, skip := deliberatelyUnmodeled[f]; skip {
				continue
			}
			if !invFlags[f] {
				problems = append(problems, cmd+" --"+f)
			}
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("oracle flags missing from cli-flags-inventory.yaml (add them to the command, or justify in deliberatelyUnmodeled): %v", problems)
	}
}

// oracleCommandFlags maps each top-level command name to the set of flag names
// its options function declares in the oracle source.
func oracleCommandFlags(src string) map[string]map[string]bool {
	// y.command('up', 'desc', provisionOptions, provisionHandler)
	cmdRe := regexp.MustCompile(`y\.command\('([a-z-]+)',\s*'[^']*',\s*(\w+Options),`)
	// A quoted kebab/lower flag key mapping to an option object: 'no-lockfile': {
	flagRe := regexp.MustCompile(`'([a-z][a-z0-9-]*)':\s*\{`)

	fnBodies := oracleOptionFnBodies(src)
	out := map[string]map[string]bool{}
	for _, m := range cmdRe.FindAllStringSubmatch(src, -1) {
		cmd, fn := m[1], m[2]
		body, ok := fnBodies[fn]
		if !ok {
			continue
		}
		flags := map[string]bool{}
		for _, fm := range flagRe.FindAllStringSubmatch(body, -1) {
			flags[fm[1]] = true
		}
		if len(flags) > 0 {
			out[cmd] = flags
		}
	}
	return out
}

// oracleOptionFnBodies returns the source text of each `function <name>Options(...)`
// block, split at top-level function boundaries.
func oracleOptionFnBodies(src string) map[string]string {
	lines := strings.Split(src, "\n")
	fnStart := regexp.MustCompile(`^(?:async )?function (\w+)\(`)
	bodies := map[string]string{}
	cur := ""
	var buf []string
	flush := func() {
		if cur != "" {
			bodies[cur] = strings.Join(buf, "\n")
		}
	}
	for _, ln := range lines {
		if m := fnStart.FindStringSubmatch(ln); m != nil {
			flush()
			cur, buf = m[1], nil
		}
		if cur != "" {
			buf = append(buf, ln)
		}
	}
	flush()
	return bodies
}

// inventoryCommandFlags loads the flag names declared per command in the YAML.
func inventoryCommandFlags(t *testing.T) map[string]map[string]bool {
	t.Helper()
	data, err := os.ReadFile("../../docs/parity/cli-flags-inventory.yaml")
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	var doc struct {
		Commands map[string]struct {
			Flags map[string]yaml.Node `yaml:"flags"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse inventory: %v", err)
	}
	out := map[string]map[string]bool{}
	for cmd, c := range doc.Commands {
		flags := map[string]bool{}
		for name := range c.Flags {
			flags[name] = true
		}
		out[cmd] = flags
	}
	return out
}
