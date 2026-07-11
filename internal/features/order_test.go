package features

import (
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func makeOCINode(id, registry, namespace, featureID string, deps ...*FNode) *FNode {
	return &FNode{
		Type:          "user-provided",
		UserFeatureID: id,
		Options:       map[string]interface{}{},
		FeatureSet: &FeatureSet{
			SourceInfo: &OCISource{
				Registry:  registry,
				Namespace: namespace,
				ID:        featureID,
				Resource:  registry + "/" + namespace + "/" + featureID,
				UserID:    id,
			},
			Features: []Feature{{ID: featureID, Version: "1.0.0"}},
		},
		DependsOn:     deps,
		InstallsAfter: []*FNode{},
	}
}

func TestComputeInstallOrder_NoDeps(t *testing.T) {
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
	c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c")

	result, err := ComputeInstallationOrder(log.Null, []*FNode{c, a, b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("result len = %d", len(result))
	}
	// Should be sorted lexicographically within the round (all have same priority)
	ids := extractIDs(result)
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("order = %v, expected [a, b, c]", ids)
	}
}

func TestComputeInstallOrder_HardDep(t *testing.T) {
	// b depends on a → a must install before b
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)

	result, err := ComputeInstallationOrder(log.Null, []*FNode{b, a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := extractIDs(result)
	if ids[0] != "a" || ids[1] != "b" {
		t.Errorf("order = %v, expected [a, b]", ids)
	}
}

func TestComputeInstallOrder_SoftDep(t *testing.T) {
	// b installsAfter a → a should install first if both present
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
	b.InstallsAfter = []*FNode{a}

	result, err := ComputeInstallationOrder(log.Null, []*FNode{b, a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := extractIDs(result)
	if ids[0] != "a" || ids[1] != "b" {
		t.Errorf("order = %v, expected [a, b]", ids)
	}
}

func TestComputeInstallOrder_SoftDepNotPresent(t *testing.T) {
	// b installsAfter x (not in worklist) → x is pruned, b installs freely
	x := makeOCINode("ghcr.io/ns/x:1", "ghcr.io", "ns", "x")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
	b.InstallsAfter = []*FNode{x}

	result, err := ComputeInstallationOrder(log.Null, []*FNode{b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("result len = %d", len(result))
	}
}

func TestComputeInstallOrder_CircularDep(t *testing.T) {
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
	a.DependsOn = []*FNode{b}
	b.DependsOn = []*FNode{a}

	_, err := ComputeInstallationOrder(log.Null, []*FNode{a, b}, nil)
	if err == nil {
		t.Error("expected circular dependency error")
	}
}

func TestComputeInstallOrder_Chain(t *testing.T) {
	// c → b → a (chain)
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)
	c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c", b)

	result, err := ComputeInstallationOrder(log.Null, []*FNode{c, b, a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := extractIDs(result)
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("order = %v, expected [a, b, c]", ids)
	}
}

func TestComputeInstallOrder_Diamond(t *testing.T) {
	// d depends on b, c; both depend on a
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)
	c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c", a)
	d := makeOCINode("ghcr.io/ns/d:1", "ghcr.io", "ns", "d", b, c)

	result, err := ComputeInstallationOrder(log.Null, []*FNode{d, c, b, a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := extractIDs(result)
	// a first, then b and c (sorted), then d
	if ids[0] != "a" {
		t.Errorf("first should be a, got %v", ids)
	}
	if ids[len(ids)-1] != "d" {
		t.Errorf("last should be d, got %v", ids)
	}
}

func TestComputeInstallOrder_OverridePriority(t *testing.T) {
	// a, b, c with no deps. Override order: [c, a] → c should install first, then a, then b
	a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
	b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
	c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c")

	result, err := ComputeInstallationOrder(log.Null, []*FNode{a, b, c}, []string{
		"ghcr.io/ns/c:1",
		"ghcr.io/ns/a:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := extractIDs(result)
	// c has priority 2, a has priority 1, b has priority 0
	// Round 1: all eligible, max priority = 2 → only c
	// Round 2: a,b eligible, max priority = 1 → only a
	// Round 3: b
	if ids[0] != "c" {
		t.Errorf("first should be c (highest override priority), got %v", ids)
	}
	if ids[1] != "a" {
		t.Errorf("second should be a, got %v", ids)
	}
	if ids[2] != "b" {
		t.Errorf("third should be b, got %v", ids)
	}
}

func TestComputeInstallOrder_Empty(t *testing.T) {
	_, err := ComputeInstallationOrder(log.Null, []*FNode{}, nil)
	if err == nil {
		t.Error("expected error for empty worklist")
	}
}

func TestComputeInstallOrder_MixedSources(t *testing.T) {
	oci := makeOCINode("ghcr.io/ns/feat:1", "ghcr.io", "ns", "feat")
	local := &FNode{
		Type:          "user-provided",
		UserFeatureID: "./local-feat",
		FeatureSet: &FeatureSet{
			SourceInfo: &LocalSource{
				LocalPath:    "./local-feat",
				ResolvedPath: "/abs/local-feat",
				UserID:       "./local-feat",
			},
			Features: []Feature{{ID: "local-feat"}},
		},
		DependsOn:     []*FNode{},
		InstallsAfter: []*FNode{},
	}

	result, err := ComputeInstallationOrder(log.Null, []*FNode{local, oci}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("result len = %d", len(result))
	}
}

func extractIDs(sets []*FeatureSet) []string {
	ids := make([]string, len(sets))
	for i, s := range sets {
		if len(s.Features) > 0 {
			ids[i] = s.Features[0].ID
		}
	}
	return ids
}
