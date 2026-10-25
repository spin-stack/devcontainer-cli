package features

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// A Feature without legacyIds has one alias, its own id, and TS
// generateMermaidNode (containerFeaturesOrder.ts) labels it with that one:
// `featureIdAliases.length > 0`.
func TestGenerateMermaidDiagramLabelsASingleAlias(t *testing.T) {
	registry := map[string]stubFeature{"ghcr.io/ns/a": {id: "a", digest: "sha256:a"}}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(GenerateMermaidDiagram(graph), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("diagram lines = %q", lines)
	}
	m := mermaidNodeLine.FindStringSubmatch(lines[1])
	if m == nil || m[2] != "ghcr.io/ns/a:1<br/><0><br>aliases: a" {
		t.Fatalf("node line = %q", lines[1])
	}
}
