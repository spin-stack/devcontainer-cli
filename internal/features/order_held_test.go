package features

import (
	"slices"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// heldSource is a SourceInfo of a kind the package does not define; TS
// compares such a (legacy) source by its user feature id.
type heldSource struct{ id string }

func (s *heldSource) SourceType() string    { return "held" }
func (s *heldSource) UserFeatureID() string { return s.id }

// sourceNode is a node of the given source; label tells the nodes apart in
// the install order.
func sourceNode(label string, src SourceInfo, options any) *FNode {
	return &FNode{
		Type:          "user-provided",
		UserFeatureID: src.UserFeatureID(),
		Options:       options,
		Set:           &Set{SourceInfo: src, Features: []Feature{{ID: label, Version: label}}},
		DependsOn:     []*FNode{},
		InstallsAfter: []*FNode{},
	}
}

func localNode(label, path string, options any) *FNode {
	return sourceNode(label, &LocalSource{LocalPath: path, ResolvedPath: path, UserID: path}, options)
}

func tarballNode(label, uri string, options any) *FNode {
	return sourceNode(label, &TarballSource{TarballURI: uri, UserID: uri}, options)
}

func heldNode(label, id string, options any) *FNode {
	return sourceNode(label, &heldSource{id: id}, options)
}

// ociTagNode is a reference to ghcr.io/ns/a with the given tag and digest.
func ociTagNode(label, tag, digest string, options any) *FNode {
	n := optionsNode(label, options)
	src := n.Set.SourceInfo.(*OCISource)
	src.Tag, src.ManifestDigest = tag, digest
	return n
}

// installOrder returns the labels of ComputeInstallationOrder's sets.
func installOrder(t *testing.T, nodes ...*FNode) []string {
	t.Helper()
	sets, err := ComputeInstallationOrder(log.Null, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sets {
		got = append(got, s.Features[0].Version)
	}
	return got
}

// A Feature installs after the one its installsAfter names, matched as TS
// satisfiesSoftDependency (containerFeaturesOrder.ts) matches it: a file-path
// Feature by resolvedFilePath, a direct-tarball one by tarballUri, any other
// by its user feature id. Without the soft dependency b would sort first.
func TestComputeInstallationOrderMatchesSoftDepsBySourceIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		node func(label, id string) *FNode
		z, b string
	}{
		{"file-path", func(l, id string) *FNode { return localNode(l, id, map[string]any{}) }, "/z", "/b"},
		{"direct-tarball", func(l, id string) *FNode { return tarballNode(l, id, map[string]any{}) }, "https://x/z.tgz", "https://x/b.tgz"},
		{"other source", func(l, id string) *FNode { return heldNode(l, id, map[string]any{}) }, "z", "b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			z, b := tt.node("z", tt.z), tt.node("b", tt.b)
			b.InstallsAfter = []*FNode{tt.node("soft", tt.z)}
			if got := installOrder(t, b, z); !slices.Equal(got, []string{"z", "b"}) {
				t.Fatalf("order = %v, want [z b]", got)
			}
		})
	}
}

// Nodes of one round sort as TS compareTo (containerFeaturesOrder.ts) orders
// them, and nodes it finds equal keep their input order (TS sorts with the
// stable Array.prototype.sort).
func TestComputeInstallationOrderBreaksTiesAsTSCompareTo(t *testing.T) {
	x, y := map[string]any{"v": "x"}, map[string]any{"v": "y"}
	none := map[string]any{}
	for _, tt := range []struct {
		name  string
		nodes []*FNode
		want  []string
	}{
		// Equal digests and options short-circuit to equal, even across tags.
		{"same digest and options", []*FNode{ociTagNode("latest", "latest", "sha256:a", none), ociTagNode("one", "1", "sha256:a", none)}, []string{"latest", "one"}},
		// Two tags sort by tag before the digest.
		{"tags before digests", []*FNode{ociTagNode("latest", "latest", "sha256:a", none), ociTagNode("one", "1", "sha256:b", none)}, []string{"one", "latest"}},
		// A missing tag leaves the order to the digest, whichever side lacks it.
		{"untagged listed first", []*FNode{ociTagNode("untagged", "", "sha256:b", none), ociTagNode("tagged", "2", "sha256:a", none)}, []string{"tagged", "untagged"}},
		{"untagged listed second", []*FNode{ociTagNode("tagged", "2", "sha256:b", none), ociTagNode("untagged", "", "sha256:a", none)}, []string{"untagged", "tagged"}},
		// One tag, equal options: the digest decides.
		{"digest last", []*FNode{ociTagNode("b", "1", "sha256:b", none), ociTagNode("a", "1", "sha256:a", none)}, []string{"a", "b"}},
		// A file-path Feature sorts by path, then by options.
		{"file-path by path", []*FNode{localNode("b", "/b", x), localNode("a", "/a", y)}, []string{"a", "b"}},
		{"file-path by options", []*FNode{localNode("y", "/a", y), localNode("x", "/a", x)}, []string{"x", "y"}},
		// A direct-tarball Feature sorts by URI, then by options.
		{"tarball by uri", []*FNode{tarballNode("b", "https://x/b.tgz", x), tarballNode("a", "https://x/a.tgz", y)}, []string{"a", "b"}},
		{"tarball by options", []*FNode{tarballNode("y", "https://x/a.tgz", y), tarballNode("x", "https://x/a.tgz", x)}, []string{"x", "y"}},
		// Any other source sorts by user feature id, then by options.
		{"other by id", []*FNode{heldNode("b", "b", x), heldNode("a", "a", y)}, []string{"a", "b"}},
		{"other by options", []*FNode{heldNode("y", "a", y), heldNode("x", "a", x)}, []string{"x", "y"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := installOrder(t, tt.nodes...); !slices.Equal(got, tt.want) {
				t.Fatalf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

// Only a hand-built graph has a node that was never resolved:
// BuildDependencyGraph gives every node it makes a Set with a source. Such a
// soft dependency matches no Feature, so it is pruned and its Feature
// installs, and a Feature without a source sorts by its user feature id.
// (TS satisfiesSoftDependency and compareTo throw "ERR: Failure resolving
// Features." for a node without sourceInformation.)
func TestComputeInstallationOrderToleratesUnresolvedNodes(t *testing.T) {
	a := localNode("a", "a", map[string]any{})
	a.InstallsAfter = []*FNode{{UserFeatureID: "no set"}, {UserFeatureID: "no source", Set: &Set{}}}
	sourceless := &FNode{UserFeatureID: "b", Set: &Set{Features: []Feature{{Version: "b"}}}}
	if got := installOrder(t, sourceless, a); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("order = %v, want [a b]", got)
	}
}

// A hard dependency that was never resolved equals no Feature, so the Feature
// that depends on it never installs and the order fails, as TS fails on it.
func TestComputeInstallationOrderFailsOnAnUnresolvedDependency(t *testing.T) {
	a := localNode("a", "a", map[string]any{})
	a.DependsOn = []*FNode{{UserFeatureID: "no set"}}
	if _, err := ComputeInstallationOrder(log.Null, []*FNode{localNode("b", "b", map[string]any{}), a}); err == nil {
		t.Fatal("installed a Feature whose dependency was never resolved")
	}
}
