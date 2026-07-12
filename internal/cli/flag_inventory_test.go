package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// TestFlagInventoryParity validates docs/parity/cli-flags-inventory.yaml against
// the live Cobra command tree so the inventory cannot silently drift from the code.
//
// The Cobra tree is the source of truth: the test walks NewRootCommand().Commands()
// recursively and, for every command, uses cmd.Flags().VisitAll to collect the
// *structural* shape of each flag — name, shorthand (alias), value type
// (pflag flag.Value.Type()), default value (flag.DefValue) and hidden. It then
// unmarshals the YAML into a mirror struct and diffs BOTH directions (a flag
// missing from the YAML, a flag present in the YAML but absent from the tree, and
// any field that differs). Any drift fails the test.
//
// Type mapping YAML -> pflag value type:
//
//	array: true       -> stringArray | stringSlice   (either is accepted)
//	type: choice      -> string   (choices are asserted to be present in the YAML,
//	                               but the enum *enforcement* is NOT reflected —
//	                               it lives in RunE via validateEnum)
//	type: boolean     -> bool
//	type: number      -> int
//	type: string      -> string
//
// What is intentionally NOT reflected (and therefore excluded from the diff):
// behavioural validations that live in RunE and are not visible on the FlagSet —
// enum enforcement (validateEnum), --terminal-columns/--terminal-rows implications,
// mount / id-label / remote-env format regexes, and required/positional semantics.
// Those belong to the behavioural parity matrix, not to a reflective flag test.
//
// Notes on tree quirks handled here:
//   - `exec` sets DisableFlagParsing:true, but its flags are still declared on the
//     FlagSet, so VisitAll observes them exactly like any other command.
//   - Commands with no flags (the `features` / `templates` group parents) have no
//     `flags:` block in the YAML; the walk recurses into their subcommands.
//   - "declared but hidden" flags (e.g. the several experimental-* booleans) are
//     reconciled via flag.Hidden against the YAML `hidden:` field.
func TestFlagInventoryParity(t *testing.T) {
	// --- reflect the live Cobra tree -----------------------------------------
	actual := map[string]map[string]reflectedFlag{}
	root := NewRootCommand()
	var walk func(cmd *cobra.Command, parent string)
	walk = func(cmd *cobra.Command, parent string) {
		path := cmd.Name()
		if parent != "" {
			path = parent + " " + cmd.Name()
		}
		flags := map[string]reflectedFlag{}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			flags[f.Name] = reflectedFlag{
				shorthand: f.Shorthand,
				typ:       f.Value.Type(),
				defValue:  f.DefValue,
				hidden:    f.Hidden,
			}
		})
		if len(flags) > 0 {
			actual[path] = flags
		}
		for _, sub := range cmd.Commands() {
			walk(sub, path)
		}
	}
	// The root command carries no user-facing flags; start from its subcommands so
	// the YAML keys map cleanly onto command paths ("up", "features test", ...).
	for _, sub := range root.Commands() {
		walk(sub, "")
	}

	// --- load the YAML mirror ------------------------------------------------
	repoRoot := findRepoRoot(t)
	yamlPath := filepath.Join(repoRoot, "docs", "parity", "cli-flags-inventory.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	var inv yamlInventory
	if err := yaml.Unmarshal(data, &inv); err != nil {
		t.Fatalf("unmarshal inventory: %v", err)
	}
	expected := map[string]map[string]yamlFlag{}
	for name, cmd := range inv.Commands {
		flattenYAMLCommand(name, cmd, expected)
	}

	// --- diff both directions ------------------------------------------------
	var problems []string
	report := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	// Commands present in the tree but missing from the YAML (and vice-versa).
	for path := range actual {
		if _, ok := expected[path]; !ok {
			report("command %q is in the Cobra tree but missing from the YAML inventory", path)
		}
	}
	for path := range expected {
		if _, ok := actual[path]; !ok {
			report("command %q is in the YAML inventory but missing from the Cobra tree", path)
		}
	}

	for _, path := range sortedKeys(actual) {
		want, ok := expected[path]
		if !ok {
			continue
		}
		got := actual[path]

		for _, name := range sortedFlagNames(got) {
			wf, ok := want[name]
			if !ok {
				report("%s: flag --%s is declared in the tree but missing from the YAML", path, name)
				continue
			}
			compareFlag(path, name, wf, got[name], report)
		}
		for _, name := range sortedYAMLFlagNames(want) {
			if _, ok := got[name]; !ok {
				report("%s: flag --%s is in the YAML but not declared in the tree", path, name)
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("flag inventory drift (%d):\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

// reflectedFlag is the structural shape read off a live *pflag.Flag.
type reflectedFlag struct {
	shorthand string
	typ       string
	defValue  string
	hidden    bool
}

// yamlFlag mirrors a single flag entry in the inventory. Only the structural
// fields are decoded; description/required/implies/positional/notes are ignored.
type yamlFlag struct {
	Type    string   `yaml:"type"`
	Alias   string   `yaml:"alias"`
	Default any      `yaml:"default"`
	Array   bool     `yaml:"array"`
	Hidden  bool     `yaml:"hidden"`
	Choices []string `yaml:"choices"`
}

type yamlCommand struct {
	Flags       map[string]yamlFlag    `yaml:"flags"`
	Subcommands map[string]yamlCommand `yaml:"subcommands"`
}

type yamlInventory struct {
	Commands map[string]yamlCommand `yaml:"commands"`
}

// flattenYAMLCommand walks the YAML command/subcommand tree into a flat
// path -> flags map keyed the same way as the reflected Cobra tree.
func flattenYAMLCommand(path string, cmd yamlCommand, out map[string]map[string]yamlFlag) {
	if len(cmd.Flags) > 0 {
		out[path] = cmd.Flags
	}
	for name, sub := range cmd.Subcommands {
		flattenYAMLCommand(path+" "+name, sub, out)
	}
}

// compareFlag diffs the structural fields of one flag and appends any drift.
func compareFlag(path, name string, want yamlFlag, got reflectedFlag, report func(string, ...any)) {
	// alias / shorthand
	if want.Alias != got.shorthand {
		report("%s: flag --%s alias mismatch: yaml=%q tree=%q", path, name, want.Alias, got.shorthand)
	}
	// hidden
	if want.Hidden != got.hidden {
		report("%s: flag --%s hidden mismatch: yaml=%v tree=%v", path, name, want.Hidden, got.hidden)
	}
	// value type
	if !typeMatches(want, got.typ) {
		report("%s: flag --%s type mismatch: yaml=%q(array=%v) => tree=%q", path, name, want.Type, want.Array, got.typ)
	}
	// choices must be present for choice flags (enum *enforcement* is not reflected).
	if want.Type == "choice" && len(want.Choices) == 0 {
		report("%s: flag --%s is type choice but has no `choices` listed in the YAML", path, name)
	}
	// default value
	if exp := expectedDefault(want); exp != got.defValue {
		report("%s: flag --%s default mismatch: yaml=%q tree=%q", path, name, exp, got.defValue)
	}
}

// typeMatches maps a YAML flag onto the acceptable pflag value type(s).
func typeMatches(f yamlFlag, treeType string) bool {
	if f.Array {
		return treeType == "stringArray" || treeType == "stringSlice"
	}
	switch f.Type {
	case "string", "choice":
		return treeType == "string"
	case "boolean":
		return treeType == "bool"
	case "number":
		return treeType == "int"
	default:
		return false
	}
}

// expectedDefault computes the pflag DefValue string implied by a YAML flag.
func expectedDefault(f yamlFlag) string {
	if f.Array {
		return "[]" // pflag renders a nil string slice/array default as "[]"
	}
	if f.Default == nil {
		switch f.Type {
		case "boolean":
			return "false"
		case "number":
			return "0"
		default:
			return ""
		}
	}
	switch v := f.Default.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func sortedKeys(m map[string]map[string]reflectedFlag) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedFlagNames(m map[string]reflectedFlag) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedYAMLFlagNames(m map[string]yamlFlag) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
