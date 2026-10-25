package features

import (
	"regexp"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// mermaidNodeLine is one node line of the diagram: TS generateMermaidNode
// (containerFeaturesOrder.ts) names a node by the first 6 hex digits of its
// sha256, then its id, roundPriority and aliases.
var mermaidNodeLine = regexp.MustCompile(`^([0-9a-f]{6})\[(.*)\]$`)

// Each resolved node is labelled with its aliases, [id, ...legacyIds], which
// TS _buildDependencyGraph stores as featureIdAliases.
func TestGenerateMermaidDiagramLabelsAliases(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", legacyIds: []string{"old-a"}},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := GenerateMermaidDiagram(graph)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "flowchart" {
		t.Fatalf("diagram = %q", out)
	}
	m := mermaidNodeLine.FindStringSubmatch(lines[1])
	if m == nil || m[2] != "ghcr.io/ns/a:1<br/><0><br>aliases: a, old-a" {
		t.Fatalf("node line = %q", lines[1])
	}
}

// Two nodes that differ only in their options are two mermaid nodes: TS hashes
// the whole node, options included.
func TestGenerateMermaidDiagramHashesOptions(t *testing.T) {
	node := func(v string) *FNode {
		return &FNode{Type: "user-provided", UserFeatureID: "ghcr.io/ns/a:1", Options: map[string]any{"v": v}}
	}
	out := GenerateMermaidDiagram([]*FNode{node("1"), node("2")})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("diagram = %q", out)
	}
	first, second := mermaidNodeLine.FindStringSubmatch(lines[1]), mermaidNodeLine.FindStringSubmatch(lines[2])
	if first == nil || second == nil {
		t.Fatalf("node lines = %q", lines[1:])
	}
	if first[1] == second[1] {
		t.Fatalf("nodes with different options share hash %s", first[1])
	}
}
