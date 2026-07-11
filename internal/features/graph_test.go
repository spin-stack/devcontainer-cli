package features

import (
	"reflect"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// stubFeature is an in-memory feature definition keyed by its OCI resource
// (registry/namespace/id, without a tag).
type stubFeature struct {
	id            string // canonical feature id (currentId)
	digest        string
	legacyIds     []string
	dependsOn     map[string]interface{}
	installsAfter []string
}

// newStubProcessFeature builds a hermetic processFeature that resolves OCI-style
// ids ("registry/ns/id:tag") from an in-memory registry keyed by resource. An id
// not present in the registry resolves to (nil, nil) — "could not be processed".
func newStubProcessFeature(registry map[string]stubFeature) ProcessFeature {
	return func(node *FNode) (*FeatureSet, error) {
		resource := StripVersionFromFeatureID(node.UserFeatureID)
		sf, ok := registry[resource]
		if !ok {
			return nil, nil
		}
		reg, ns, id := splitResource(resource)
		tag := ""
		if idx := strings.LastIndex(node.UserFeatureID, ":"); idx > 0 {
			tag = node.UserFeatureID[idx+1:]
		}
		featID := sf.id
		if featID == "" {
			featID = id
		}
		return &FeatureSet{
			SourceInfo: &OCISource{
				Type:           "oci",
				Registry:       reg,
				Namespace:      ns,
				ID:             id,
				Resource:       resource,
				Tag:            tag,
				ManifestDigest: sf.digest,
				UserID:         node.UserFeatureID,
			},
			Features: []Feature{{
				ID:            featID,
				LegacyIds:     sf.legacyIds,
				DependsOn:     sf.dependsOn,
				InstallsAfter: sf.installsAfter,
				Value:         node.Options,
			}},
			ComputedDigest: sf.digest,
		}, nil
	}
}

func splitResource(resource string) (registry, namespace, id string) {
	parts := strings.Split(resource, "/")
	if len(parts) < 2 {
		return resource, "", resource
	}
	registry = parts[0]
	id = parts[len(parts)-1]
	namespace = strings.Join(parts[1:len(parts)-1], "/")
	return registry, namespace, id
}

func userFeats(ids ...string) []DevContainerFeature {
	out := make([]DevContainerFeature, 0, len(ids))
	for _, id := range ids {
		out = append(out, DevContainerFeature{UserFeatureID: id, Options: map[string]interface{}{}})
	}
	return out
}

func orderIDs(t *testing.T, graph []*FNode, override []string) []string {
	t.Helper()
	sets, err := ComputeInstallationOrder(log.Null, graph, override)
	if err != nil {
		t.Fatalf("ComputeInstallationOrder: %v", err)
	}
	return extractIDs(sets)
}

func TestBuildDependencyGraph_TransitiveDependsOn(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", dependsOn: map[string]interface{}{"ghcr.io/ns/b:1": map[string]interface{}{}}},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b", dependsOn: map[string]interface{}{"ghcr.io/ns/c:1": map[string]interface{}{}}},
		"ghcr.io/ns/c": {id: "c", digest: "sha256:c"},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The worklist should contain a, b and c (transitively resolved).
	if len(graph) != 3 {
		t.Fatalf("worklist size = %d, want 3", len(graph))
	}
	if got, want := orderIDs(t, graph, nil), []string{"c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildDependencyGraph_DiamondDedup(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/d": {id: "d", digest: "sha256:d", dependsOn: map[string]interface{}{
			"ghcr.io/ns/b:1": map[string]interface{}{},
			"ghcr.io/ns/c:1": map[string]interface{}{},
		}},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b", dependsOn: map[string]interface{}{"ghcr.io/ns/a:1": map[string]interface{}{}}},
		"ghcr.io/ns/c": {id: "c", digest: "sha256:c", dependsOn: map[string]interface{}{"ghcr.io/ns/a:1": map[string]interface{}{}}},
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a"},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/d:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// a reached through both b and c but must appear once in the worklist.
	if len(graph) != 4 {
		t.Fatalf("worklist size = %d, want 4 (a deduped)", len(graph))
	}
	if got, want := orderIDs(t, graph, nil), []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildDependencyGraph_DedupByOptions(t *testing.T) {
	// Same resource+digest but different options → two distinct nodes.
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a"},
	}
	uf := []DevContainerFeature{
		{UserFeatureID: "ghcr.io/ns/a:1", Options: map[string]interface{}{"x": "1"}},
		{UserFeatureID: "ghcr.io/ns/a:1", Options: map[string]interface{}{"x": "2"}},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), uf, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph) != 2 {
		t.Fatalf("worklist size = %d, want 2 (distinct options)", len(graph))
	}
}

func TestBuildDependencyGraph_SoftDepPruned(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b", installsAfter: []string{"ghcr.io/ns/x:1"}},
		"ghcr.io/ns/x": {id: "x", digest: "sha256:x"},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/b:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// x is processFeature'd (to read legacyIds) but NOT recursively expanded, so
	// it is not part of the worklist; the dangling soft-dep is pruned.
	if len(graph) != 1 {
		t.Fatalf("worklist size = %d, want 1 (soft dep not added)", len(graph))
	}
	if got, want := orderIDs(t, graph, nil), []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildDependencyGraph_SoftDepOrders(t *testing.T) {
	// When the soft dep IS present, it installs first.
	registry := map[string]stubFeature{
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b", installsAfter: []string{"ghcr.io/ns/a:1"}},
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a"},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1", "ghcr.io/ns/b:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, graph, nil), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildDependencyGraph_SoftDepAliasMatch(t *testing.T) {
	// b installsAfter the legacy id "nodejs"; the installed feature's resource is
	// "node" with legacyIds ["nodejs"]. The soft dep must match via the alias.
	registry := map[string]stubFeature{
		"ghcr.io/ns/node":   {id: "node", digest: "sha256:node", legacyIds: []string{"nodejs"}},
		"ghcr.io/ns/nodejs": {id: "node", digest: "sha256:node", legacyIds: []string{"nodejs"}},
		"ghcr.io/ns/b":      {id: "b", digest: "sha256:b", installsAfter: []string{"ghcr.io/ns/nodejs:1"}},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/node:1", "ghcr.io/ns/b:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, graph, nil), []string{"node", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v (alias match should order node before b)", got, want)
	}
}

func TestBuildDependencyGraph_Cycle(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", dependsOn: map[string]interface{}{"ghcr.io/ns/b:1": map[string]interface{}{}}},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b", dependsOn: map[string]interface{}{"ghcr.io/ns/a:1": map[string]interface{}{}}},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ComputeInstallationOrder(log.Null, graph, nil); err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestBuildDependencyGraph_UnprocessableAborts(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", dependsOn: map[string]interface{}{"ghcr.io/ns/missing:1": map[string]interface{}{}}},
	}
	_, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err == nil {
		t.Fatal("expected error for unprocessable dependency")
	}
}

func TestBuildDependencyGraph_OverridePriority(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a"},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b"},
		"ghcr.io/ns/c": {id: "c", digest: "sha256:c"},
	}
	override := []string{"ghcr.io/ns/c:1", "ghcr.io/ns/a:1"}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1", "ghcr.io/ns/b:1", "ghcr.io/ns/c:1"), override, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Override is applied during graph build; pass nil to ComputeInstallationOrder.
	if got, want := orderIDs(t, graph, nil), []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildDependencyGraph_OverrideAliasMatch(t *testing.T) {
	// Override references the legacy id; it must match the canonical feature.
	registry := map[string]stubFeature{
		"ghcr.io/ns/node":   {id: "node", digest: "sha256:node", legacyIds: []string{"nodejs"}},
		"ghcr.io/ns/nodejs": {id: "node", digest: "sha256:node", legacyIds: []string{"nodejs"}},
		"ghcr.io/ns/b":      {id: "b", digest: "sha256:b"},
	}
	override := []string{"ghcr.io/ns/nodejs:1"}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/b:1", "ghcr.io/ns/node:1"), override, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, graph, nil), []string{"node", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v (override alias should prioritize node)", got, want)
	}
}

func TestBuildDependencyGraph_OverrideInvalidAborts(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a"},
	}
	override := []string{"ghcr.io/ns/missing:1"}
	_, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), override, nil)
	if err == nil {
		t.Fatal("expected error for invalid overrideFeatureInstallOrder entry")
	}
}

func TestGenerateMermaidDiagram(t *testing.T) {
	registry := map[string]stubFeature{
		"ghcr.io/ns/a": {id: "a", digest: "sha256:a", dependsOn: map[string]interface{}{"ghcr.io/ns/b:1": map[string]interface{}{}}},
		"ghcr.io/ns/b": {id: "b", digest: "sha256:b"},
	}
	graph, err := BuildDependencyGraph(log.Null, newStubProcessFeature(registry), userFeats("ghcr.io/ns/a:1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := GenerateMermaidDiagram(graph)
	if !strings.HasPrefix(out, "flowchart\n") {
		t.Fatalf("mermaid output missing flowchart header:\n%s", out)
	}
	if !strings.Contains(out, "-->") {
		t.Fatalf("mermaid output missing hard-dependency edge:\n%s", out)
	}
	if !strings.Contains(out, "ghcr.io/ns/a:1") || !strings.Contains(out, "ghcr.io/ns/b:1") {
		t.Fatalf("mermaid output missing node labels:\n%s", out)
	}
}
