package cli

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOracleFlagCoverage(t *testing.T) {
	const oraclePath = "../../reference/src/spec-node/devContainersSpecCLI.ts"
	src, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Skipf("oracle not checked out (%s); run `task reference`", oraclePath)
	}

	deliberatelyUnmodeled := map[string]string{}

	oracle := oracleCommandFlags(string(src))
	inventory := inventoryCommandFlags(t)

	var problems []string

	inventoryGlobals := inventoryGlobalFlags(t)
	for f := range oracleGlobalFlags(string(src)) {
		if !inventoryGlobals[f] {
			problems = append(problems, "<global> --"+f)
		}
	}
	for cmd, flags := range oracle {
		invFlags, ok := inventory[cmd]
		if !ok {
			continue
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

func oracleGlobalFlags(src string) map[string]bool {
	start := strings.Index(src, ".scriptName('devcontainer')")
	end := strings.Index(src, ".strict();")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	chain := src[start:end]
	optionRe := regexp.MustCompile(`\.option\('([a-z][a-z0-9-]*)',\s*\{`)
	out := map[string]bool{}
	for _, m := range optionRe.FindAllStringSubmatch(chain, -1) {
		out[m[1]] = true
	}
	return out
}

func inventoryGlobalFlags(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("../../docs/parity/cli-flags-inventory.yaml")
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	var doc struct {
		GlobalFlags map[string]yaml.Node `yaml:"global_flags"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse inventory: %v", err)
	}
	out := map[string]bool{}
	for name := range doc.GlobalFlags {
		out[name] = true
	}
	return out
}

func oracleCommandFlags(src string) map[string]map[string]bool {
	cmdRe := regexp.MustCompile(`y\.command\('([a-z-]+)',\s*'[^']*',\s*(\w+Options),`)
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
