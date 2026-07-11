package features

import (
	"reflect"
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

func TestComputeInstallOrder(t *testing.T) {
	tests := []struct {
		name      string
		build     func() []*FNode
		override  []string
		wantErr   bool
		wantOrder []string
	}{
		{
			// Should be sorted lexicographically within the round (all have same priority).
			name: "NoDeps",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
				c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c")
				return []*FNode{c, a, b}
			},
			wantOrder: []string{"a", "b", "c"},
		},
		{
			// b depends on a → a must install before b.
			name: "HardDep",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)
				return []*FNode{b, a}
			},
			wantOrder: []string{"a", "b"},
		},
		{
			// b installsAfter a → a should install first if both present.
			name: "SoftDep",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
				b.InstallsAfter = []*FNode{a}
				return []*FNode{b, a}
			},
			wantOrder: []string{"a", "b"},
		},
		{
			// b installsAfter x (not in worklist) → x is pruned, b installs freely.
			name: "SoftDepNotPresent",
			build: func() []*FNode {
				x := makeOCINode("ghcr.io/ns/x:1", "ghcr.io", "ns", "x")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
				b.InstallsAfter = []*FNode{x}
				return []*FNode{b}
			},
			wantOrder: []string{"b"},
		},
		{
			name: "CircularDep",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
				a.DependsOn = []*FNode{b}
				b.DependsOn = []*FNode{a}
				return []*FNode{a, b}
			},
			wantErr: true,
		},
		{
			// c → b → a (chain).
			name: "Chain",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)
				c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c", b)
				return []*FNode{c, b, a}
			},
			wantOrder: []string{"a", "b", "c"},
		},
		{
			// d depends on b, c; both depend on a. a first, then b and c (sorted), then d.
			name: "Diamond",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b", a)
				c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c", a)
				d := makeOCINode("ghcr.io/ns/d:1", "ghcr.io", "ns", "d", b, c)
				return []*FNode{d, c, b, a}
			},
			wantOrder: []string{"a", "b", "c", "d"},
		},
		{
			// a, b, c with no deps. Override order: [c, a].
			// c has priority 2, a has priority 1, b has priority 0.
			// Round 1: all eligible, max priority = 2 → only c.
			// Round 2: a,b eligible, max priority = 1 → only a.
			// Round 3: b.
			name: "OverridePriority",
			build: func() []*FNode {
				a := makeOCINode("ghcr.io/ns/a:1", "ghcr.io", "ns", "a")
				b := makeOCINode("ghcr.io/ns/b:1", "ghcr.io", "ns", "b")
				c := makeOCINode("ghcr.io/ns/c:1", "ghcr.io", "ns", "c")
				return []*FNode{a, b, c}
			},
			override:  []string{"ghcr.io/ns/c:1", "ghcr.io/ns/a:1"},
			wantOrder: []string{"c", "a", "b"},
		},
		{
			name: "Empty",
			build: func() []*FNode {
				return []*FNode{}
			},
			wantErr: true,
		},
		{
			name: "MixedSources",
			build: func() []*FNode {
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
				return []*FNode{local, oci}
			},
			wantOrder: []string{"local-feat", "feat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ComputeInstallationOrder(log.Null, tt.build(), tt.override)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ids := extractIDs(result)
			if !reflect.DeepEqual(ids, tt.wantOrder) {
				t.Errorf("order = %v, expected %v", ids, tt.wantOrder)
			}
		})
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
