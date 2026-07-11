package features

import (
	"fmt"
	"sort"

	"github.com/devcontainers/cli/internal/log"
)

// ProcessFeature resolves a single feature (identified by node.UserFeatureID and
// node.Options) into a FeatureSet. The returned FeatureSet must have its
// SourceInfo populated and Features[0] carrying the feature metadata
// (id/legacyIds/dependsOn/installsAfter), read annotation-first with a blob
// fallback for OCI features. Returning (nil, nil) signals "could not be
// processed" (mirrors the TS closure returning undefined) — the builder turns
// that into the appropriate error.
//
// This is the injected seam that the TS CLI calls `processFeature`. All three
// Go consumers (install, resolve-dependencies, mermaid) supply their own
// implementation: the install path fetches and extracts the blob so the feature
// can be built, while the read-only consumers only fetch enough to read metadata.
type ProcessFeature func(node *FNode) (*FeatureSet, error)

// BuildDependencyGraph is the Go port of buildDependencyGraph from
// containerFeaturesOrder.ts. It walks the user-provided features, recursively
// resolving hard dependencies (dependsOn) and processing — but not recursively
// expanding — soft dependencies (installsAfter), returning the resolved worklist
// of FNodes with both edge kinds wired. overrideFeatureInstallOrder priorities
// are baked into each node's RoundPriority.
//
// The returned worklist is the "precomputed graph" accepted by
// ComputeInstallationOrder (pass a nil override there — priorities are already
// applied here) and by GenerateMermaidDiagram.
func BuildDependencyGraph(
	logger log.Log,
	processFeature ProcessFeature,
	userFeatures []DevContainerFeature,
	overrideOrder []string,
	lockfile *Lockfile,
) ([]*FNode, error) {
	rootNodes := make([]*FNode, 0, len(userFeatures))
	for _, f := range userFeatures {
		rootNodes = append(rootNodes, &FNode{
			Type:          "user-provided",
			UserFeatureID: f.UserFeatureID,
			Options:       f.Options,
			DependsOn:     []*FNode{},
			InstallsAfter: []*FNode{},
		})
	}

	worklist, err := resolveDependencyGraph(logger, processFeature, rootNodes)
	if err != nil {
		return nil, err
	}

	// Apply overrideFeatureInstallOrder. Matching is by OCI identity + legacyIds
	// (via processFeature + satisfiesSoftDependency), and invalid entries abort —
	// mirroring the TS applyOverrideFeatureInstallOrder.
	if len(overrideOrder) > 0 {
		if err := applyOverrideFeatureInstallOrder(logger, processFeature, worklist, overrideOrder); err != nil {
			return nil, err
		}
	}

	return worklist, nil
}

// resolveDependencyGraph is the Go port of the internal _buildDependencyGraph.
// It processes a FIFO worklist, appending hard-dependency nodes back onto the
// worklist for recursive resolution while processing soft-dependency nodes once
// (only to read their legacyIds). Duplicate nodes (same resource + options) are
// skipped without being re-added; genuine cycles are surfaced later by
// ComputeInstallationOrder.
func resolveDependencyGraph(
	logger log.Log,
	processFeature ProcessFeature,
	rootNodes []*FNode,
) ([]*FNode, error) {
	worklist := append([]*FNode{}, rootNodes...)
	var acc []*FNode

	for len(worklist) > 0 {
		current := worklist[0]
		worklist = worklist[1:]

		logger.Write(fmt.Sprintf("Resolving Feature dependencies for '%s'...", current.UserFeatureID), log.LevelInfo)

		processed, err := processFeature(current)
		if err != nil {
			return nil, err
		}
		if processed == nil {
			return nil, fmt.Errorf("ERR: Feature '%s' could not be processed.  You may not have permission to access this Feature, or may not be logged in.  If the issue persists, report this to the Feature author.", current.UserFeatureID)
		}
		current.FeatureSet = processed

		// Already in the accumulator? Skip. This stops cycles from looping here;
		// they are reported by the round-based order calculation instead.
		if containsEqual(acc, current) {
			continue
		}

		if len(processed.Features) > 0 {
			feat := processed.Features[0]

			// Remember legacyIds: [<currentId>, <...legacyIds>].
			current.FeatureIDAliases = aliasesFor(feat)

			// Hard dependencies: add a node for each and push onto the worklist so
			// they are resolved recursively. Iterate in a deterministic order (Go
			// maps are unordered) — the round sort makes the final order stable, but
			// dedup "first wins" and mermaid edge order depend on this.
			depIDs := make([]string, 0, len(feat.DependsOn))
			for id := range feat.DependsOn {
				depIDs = append(depIDs, id)
			}
			sort.Strings(depIDs)
			for _, depID := range depIDs {
				dep := &FNode{
					Type:          "resolved",
					UserFeatureID: depID,
					Options:       feat.DependsOn[depID],
					DependsOn:     []*FNode{},
					InstallsAfter: []*FNode{},
				}
				current.DependsOn = append(current.DependsOn, dep)
				worklist = append(worklist, dep)
			}

			// Soft dependencies: add a node for each but do NOT push onto the
			// worklist (not recursively expanded). Still processFeature'd so their
			// legacyIds are available for soft-dependency matching.
			for _, iaID := range feat.InstallsAfter {
				dep := &FNode{
					Type:          "resolved",
					UserFeatureID: iaID,
					Options:       map[string]interface{}{},
					DependsOn:     []*FNode{},
					InstallsAfter: []*FNode{},
				}
				softSet, err := processFeature(dep)
				if err != nil {
					return nil, err
				}
				if softSet == nil {
					return nil, fmt.Errorf("installsAfter dependency '%s' of Feature '%s' could not be processed.", iaID, current.UserFeatureID)
				}
				dep.FeatureSet = softSet
				if len(softSet.Features) > 0 {
					dep.FeatureIDAliases = aliasesFor(softSet.Features[0])
				}
				current.InstallsAfter = append(current.InstallsAfter, dep)
			}
		}

		acc = append(acc, current)
	}

	return acc, nil
}

// applyOverrideFeatureInstallOrder is the Go port of the TS function of the same
// name. For each override id it processFeature's an override node, then raises
// the RoundPriority of every worklist node that satisfies it as a soft
// dependency (OCI identity + legacyIds). An override id that cannot be processed
// aborts the build, unlike the previous string-prefix matcher which ignored it.
func applyOverrideFeatureInstallOrder(
	logger log.Log,
	processFeature ProcessFeature,
	worklist []*FNode,
	overrideOrder []string,
) error {
	originalLength := len(overrideOrder)
	for i := originalLength - 1; i >= 0; i-- {
		overrideID := overrideOrder[i]
		// First element == N, last element == 1.
		roundPriority := originalLength - i

		override := &FNode{
			Type:          "override",
			UserFeatureID: overrideID,
			Options:       map[string]interface{}{},
			RoundPriority: roundPriority,
			DependsOn:     []*FNode{},
			InstallsAfter: []*FNode{},
		}
		processed, err := processFeature(override)
		if err != nil {
			return err
		}
		if processed == nil {
			return fmt.Errorf("Feature '%s' in 'overrideFeatureInstallOrder' could not be processed.", overrideID)
		}
		override.FeatureSet = processed
		if len(processed.Features) > 0 {
			override.FeatureIDAliases = aliasesFor(processed.Features[0])
		}

		for _, node := range worklist {
			if nodeSatisfiesSoftDep(node, override) {
				if roundPriority > node.RoundPriority {
					logger.Write(fmt.Sprintf("[override]: '%s' has override priority of %d", node.UserFeatureID, roundPriority), log.LevelTrace)
					node.RoundPriority = roundPriority
				}
			}
		}
	}
	return nil
}

// aliasesFor returns [<id>, <...legacyIds>] for a feature, the alias set used for
// legacyId-aware soft-dependency and override matching.
func aliasesFor(feat Feature) []string {
	aliases := make([]string, 0, 1+len(feat.LegacyIds))
	if feat.ID != "" {
		aliases = append(aliases, feat.ID)
	}
	aliases = append(aliases, feat.LegacyIds...)
	return aliases
}

func containsEqual(nodes []*FNode, n *FNode) bool {
	for _, existing := range nodes {
		if nodesEqual(existing, n) {
			return true
		}
	}
	return false
}
