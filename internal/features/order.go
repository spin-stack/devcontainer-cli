package features

import (
	"fmt"
	"sort"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

// FNode is a node in the feature dependency graph.
type FNode struct {
	Type             string // "user-provided", "override", "resolved"
	UserFeatureID    string
	Options          interface{}
	Set              *Set
	DependsOn        []*FNode
	InstallsAfter    []*FNode
	FeatureIDAliases []string
	RoundPriority    int
}

// ComputeInstallationOrder takes resolved feature sets and computes the
// topological installation order respecting dependsOn (hard) and
// installsAfter (soft) constraints, plus overrideFeatureInstallOrder priority.
//
// This is the Go port of computeDependsOnInstallationOrder from containerFeaturesOrder.ts.
func ComputeInstallationOrder(
	logger log.Logger,
	nodes []*FNode,
	overrideOrder []string,
) ([]*Set, error) {

	if len(nodes) == 0 {
		return nil, fmt.Errorf("empty worklist")
	}

	// Verify all nodes have resolved feature sets
	for _, n := range nodes {
		if n.Set == nil {
			return nil, fmt.Errorf("node %q has no resolved Set", n.UserFeatureID)
		}
	}

	// Apply override install order priorities
	if len(overrideOrder) > 0 {
		applyOverridePriority(logger, nodes, overrideOrder)
	}

	// Prune irrelevant soft dependencies:
	// Remove installsAfter edges where the soft dep doesn't match any node in the worklist.
	for _, node := range nodes {
		pruned := make([]*FNode, 0, len(node.InstallsAfter))
		for _, softDep := range node.InstallsAfter {
			if anyNodeSatisfiesSoftDep(nodes, softDep) {
				pruned = append(pruned, softDep)
			} else {
				logger.Write(fmt.Sprintf("Soft-dependency %q is not required. Removing.", softDep.UserFeatureID), log.LevelInfo)
			}
		}
		node.InstallsAfter = pruned
	}

	// Round-based topological sort
	worklist := make([]*FNode, len(nodes))
	copy(worklist, nodes)

	var installOrder []*FNode

	for len(worklist) > 0 {
		// Find nodes whose deps are all satisfied
		var round []*FNode
		for _, node := range worklist {
			if canInstall(node, installOrder) {
				round = append(round, node)
			}
		}

		if len(round) == 0 {
			remaining := make([]string, len(worklist))
			for i, n := range worklist {
				remaining[i] = n.UserFeatureID
			}
			return nil, fmt.Errorf("circular dependency detected among: %s", strings.Join(remaining, ", "))
		}

		// Filter to highest priority in this round
		maxPriority := 0
		for _, n := range round {
			if n.RoundPriority > maxPriority {
				maxPriority = n.RoundPriority
			}
		}
		if maxPriority > 0 {
			filtered := make([]*FNode, 0)
			for _, n := range round {
				if n.RoundPriority == maxPriority {
					filtered = append(filtered, n)
				}
			}
			round = filtered
		}

		// Remove round nodes from worklist
		worklist = removeNodes(worklist, round)

		// Sort round lexicographically
		sort.Slice(round, func(i, j int) bool {
			return compareNodes(round[i], round[j]) < 0
		})

		installOrder = append(installOrder, round...)
	}

	// Extract FeatureSets
	result := make([]*Set, len(installOrder))
	for i, n := range installOrder {
		result[i] = n.Set
	}
	return result, nil
}

// --- Override priority ---

func applyOverridePriority(logger log.Logger, nodes []*FNode, overrideOrder []string) {
	for i := len(overrideOrder) - 1; i >= 0; i-- {
		overrideID := overrideOrder[i]
		priority := len(overrideOrder) - i

		for _, node := range nodes {
			if matchesSoftDep(node, overrideID) {
				if priority > node.RoundPriority {
					logger.Write(fmt.Sprintf("[override]: %q gets priority %d", node.UserFeatureID, priority), log.LevelTrace)
					node.RoundPriority = priority
				}
			}
		}
	}
}

// --- Dependency checks ---

func canInstall(node *FNode, installed []*FNode) bool {
	// All hard deps must be installed
	for _, dep := range node.DependsOn {
		if !isInstalled(dep, installed) {
			return false
		}
	}
	// All soft deps must be installed (if they are in the worklist)
	for _, dep := range node.InstallsAfter {
		if !isSoftDepSatisfied(dep, installed) {
			return false
		}
	}
	return true
}

func isInstalled(dep *FNode, installed []*FNode) bool {
	for _, n := range installed {
		if nodesEqual(n, dep) {
			return true
		}
	}
	return false
}

func isSoftDepSatisfied(dep *FNode, installed []*FNode) bool {
	for _, n := range installed {
		if nodeSatisfiesSoftDep(n, dep) {
			return true
		}
	}
	return false
}

func anyNodeSatisfiesSoftDep(nodes []*FNode, softDep *FNode) bool {
	for _, n := range nodes {
		if nodeSatisfiesSoftDep(n, softDep) {
			return true
		}
	}
	return false
}

// nodeSatisfiesSoftDep checks if `node` satisfies `softDep` as a soft dependency.
// Matching is by resource identity (not options/digest), accounting for legacyIds.
func nodeSatisfiesSoftDep(node, softDep *FNode) bool {
	if node.Set == nil || softDep.Set == nil {
		return false
	}
	nSrc := node.Set.SourceInfo
	sSrc := softDep.Set.SourceInfo
	if nSrc == nil || sSrc == nil {
		return false
	}
	if nSrc.SourceType() != sSrc.SourceType() {
		return false
	}

	switch ns := nSrc.(type) {
	case *OCISource:
		ss := sSrc.(*OCISource)
		if ns.Resource == ss.Resource {
			return true
		}
		// Check legacy ID aliases
		ssPrefix := ss.Registry + "/" + ss.Namespace
		for _, alias := range softDep.FeatureIDAliases {
			if ssPrefix+"/"+alias == ns.Resource {
				return true
			}
		}
		return false

	case *LocalSource:
		ss := sSrc.(*LocalSource)
		return ns.ResolvedPath == ss.ResolvedPath

	case *TarballSource:
		ss := sSrc.(*TarballSource)
		return ns.TarballURI == ss.TarballURI

	default:
		return nSrc.UserFeatureID() == sSrc.UserFeatureID()
	}
}

// nodesEqual checks if two nodes represent the same feature (same source + options).
func nodesEqual(a, b *FNode) bool {
	if a.Set == nil || b.Set == nil {
		return false
	}
	return compareNodes(a, b) == 0
}

// compareNodes compares two nodes for sorting.
// Returns 0 if equal, <0 if a sorts before b, >0 if after.
func compareNodes(a, b *FNode) int {
	if a.Set == nil || b.Set == nil {
		return strings.Compare(a.UserFeatureID, b.UserFeatureID)
	}
	aSrc := a.Set.SourceInfo
	bSrc := b.Set.SourceInfo
	if aSrc == nil || bSrc == nil {
		return strings.Compare(a.UserFeatureID, b.UserFeatureID)
	}
	if aSrc.SourceType() != bSrc.SourceType() {
		return strings.Compare(aSrc.UserFeatureID(), bSrc.UserFeatureID())
	}

	switch as := aSrc.(type) {
	case *OCISource:
		bs := bSrc.(*OCISource)
		// Short-circuit if digests AND options match (dedup identity is
		// resource+options, not the input string).
		if as.ManifestDigest == bs.ManifestDigest && as.ManifestDigest != "" &&
			optionsCompareTo(a.Options, b.Options) == 0 {
			return 0
		}
		// Compare by resource (accounting for aliases)
		aRes := as.Registry + "/" + as.Namespace + "/" + as.ID
		bRes := bs.Registry + "/" + bs.Namespace + "/" + bs.ID
		if c := strings.Compare(aRes, bRes); c != 0 {
			return c
		}
		if as.Tag != "" && bs.Tag != "" && as.Tag != bs.Tag {
			return strings.Compare(as.Tag, bs.Tag)
		}
		if c := optionsCompareTo(a.Options, b.Options); c != 0 {
			return c
		}
		return strings.Compare(as.ManifestDigest, bs.ManifestDigest)

	case *LocalSource:
		bs := bSrc.(*LocalSource)
		if c := strings.Compare(as.ResolvedPath, bs.ResolvedPath); c != 0 {
			return c
		}
		return optionsCompareTo(a.Options, b.Options)

	case *TarballSource:
		bs := bSrc.(*TarballSource)
		if c := strings.Compare(as.TarballURI, bs.TarballURI); c != 0 {
			return c
		}
		return optionsCompareTo(a.Options, b.Options)

	default:
		if c := strings.Compare(aSrc.UserFeatureID(), bSrc.UserFeatureID()); c != 0 {
			return c
		}
		return optionsCompareTo(a.Options, b.Options)
	}
}

// optionsCompareTo compares two feature option values (string | bool |
// map[string]interface{}), porting the TS optionsCompareTo. It underpins the
// resource+options dedup identity so two references to the same Feature with
// different options are treated as distinct.
func optionsCompareTo(a, b interface{}) int {
	as, aStr := a.(string)
	bs, bStr := b.(string)
	if aStr && bStr {
		return strings.Compare(as, bs)
	}

	ab, aBool := a.(bool)
	bb, bBool := b.(bool)
	if aBool && bBool {
		return boolCompare(ab, bb)
	}

	am, aMap := a.(map[string]interface{})
	bm, bMap := b.(map[string]interface{})
	if aMap && bMap {
		if len(am) != len(bm) {
			return len(am) - len(bm)
		}
		aKeys := make([]string, 0, len(am))
		for k := range am {
			aKeys = append(aKeys, k)
		}
		bKeys := make([]string, 0, len(bm))
		for k := range bm {
			bKeys = append(bKeys, k)
		}
		sort.Strings(aKeys)
		sort.Strings(bKeys)
		for i := range aKeys {
			if aKeys[i] != bKeys[i] {
				return strings.Compare(aKeys[i], bKeys[i])
			}
			av := am[aKeys[i]]
			bv := bm[bKeys[i]]
			avs, avStr := av.(string)
			bvs, bvStr := bv.(string)
			if avStr && bvStr {
				if c := strings.Compare(avs, bvs); c != 0 {
					return c
				}
			}
			avb, avBool := av.(bool)
			bvb, bvBool := bv.(bool)
			if avBool && bvBool {
				if c := boolCompare(avb, bvb); c != 0 {
					return c
				}
			}
			// TS treats a missing/undefined value as sorting after a present one.
			if av == nil || bv == nil {
				if c := undefinedCompare(av, bv); c != 0 {
					return c
				}
			}
		}
		return 0
	}

	return strings.Compare(typeName(a), typeName(b))
}

func boolCompare(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

func undefinedCompare(a, b interface{}) int {
	if a == b {
		return 0
	}
	if a == nil {
		return 1
	}
	return -1
}

// typeName mirrors the JS `typeof` used by optionsCompareTo's final fallback.
func typeName(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "undefined"
	default:
		return "object"
	}
}

func matchesSoftDep(node *FNode, overrideID string) bool {
	if node.Set == nil {
		return false
	}
	src := node.Set.SourceInfo
	if src == nil {
		return false
	}
	strippedOverride := StripVersionFromID(overrideID)
	strippedNode := StripVersionFromID(src.UserFeatureID())
	return strippedOverride == strippedNode
}

func removeNodes(worklist []*FNode, toRemove []*FNode) []*FNode {
	result := make([]*FNode, 0, len(worklist))
	for _, n := range worklist {
		remove := false
		for _, r := range toRemove {
			if nodesEqual(n, r) {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, n)
		}
	}
	return result
}
