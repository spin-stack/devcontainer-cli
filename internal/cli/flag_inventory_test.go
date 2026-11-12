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

func TestFlagInventoryParity(t *testing.T) {
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
	for _, sub := range root.Commands() {
		walk(sub, "")
	}

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

	var problems []string
	report := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

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

	actualGlobals := map[string]reflectedFlag{}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		actualGlobals[f.Name] = reflectedFlag{
			shorthand: f.Shorthand,
			typ:       f.Value.Type(),
			defValue:  f.DefValue,
			hidden:    f.Hidden,
		}
	})
	for _, name := range sortedFlagNames(actualGlobals) {
		wf, ok := inv.GlobalFlags[name]
		if !ok {
			report("global flag --%s is declared on the root command but missing from the YAML", name)
			continue
		}
		compareFlag("<global>", name, wf, actualGlobals[name], report)
	}
	for _, name := range sortedYAMLFlagNames(inv.GlobalFlags) {
		if _, ok := actualGlobals[name]; !ok {
			report("global flag --%s is in the YAML but not declared on the root command", name)
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("flag inventory drift (%d):\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

type reflectedFlag struct {
	shorthand string
	typ       string
	defValue  string
	hidden    bool
}

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
	Commands    map[string]yamlCommand `yaml:"commands"`
	GlobalFlags map[string]yamlFlag    `yaml:"global_flags"`
}

func flattenYAMLCommand(path string, cmd yamlCommand, out map[string]map[string]yamlFlag) {
	if len(cmd.Flags) > 0 {
		out[path] = cmd.Flags
	}
	for name, sub := range cmd.Subcommands {
		flattenYAMLCommand(path+" "+name, sub, out)
	}
}

func compareFlag(path, name string, want yamlFlag, got reflectedFlag, report func(string, ...any)) {
	if want.Alias != got.shorthand {
		report("%s: flag --%s alias mismatch: yaml=%q tree=%q", path, name, want.Alias, got.shorthand)
	}
	if want.Hidden != got.hidden {
		report("%s: flag --%s hidden mismatch: yaml=%v tree=%v", path, name, want.Hidden, got.hidden)
	}
	if !typeMatches(want, got.typ) {
		report("%s: flag --%s type mismatch: yaml=%q(array=%v) => tree=%q", path, name, want.Type, want.Array, got.typ)
	}
	if want.Type == "choice" && len(want.Choices) == 0 {
		report("%s: flag --%s is type choice but has no `choices` listed in the YAML", path, name)
	}
	if exp := expectedDefault(want); exp != got.defValue {
		report("%s: flag --%s default mismatch: yaml=%q tree=%q", path, name, exp, got.defValue)
	}
}

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
