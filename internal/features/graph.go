package features

import (
	"fmt"
	"maps"
	"slices"

	"github.com/devcontainers/cli/internal/log"
)

type unprocessedFeatureError struct{ msg string }

func unprocessedFeature(format string, args ...any) error {
	return &unprocessedFeatureError{msg: fmt.Sprintf(format, args...)}
}

func (e *unprocessedFeatureError) Error() string { return e.msg }

func (e *unprocessedFeatureError) ReferenceError() {}

type ProcessFeature func(node *FNode) (*Set, error)

func BuildDependencyGraph(
	logger log.Logger,
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

	if len(overrideOrder) > 0 {
		if err := applyOverrideFeatureInstallOrder(logger, processFeature, worklist, overrideOrder); err != nil {
			return nil, err
		}
	}

	return worklist, nil
}

func resolveDependencyGraph(
	logger log.Logger,
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
			return nil, unprocessedFeature("ERR: Feature '%s' could not be processed.  You may not have permission to access this Feature, or may not be logged in.  If the issue persists, report this to the Feature author.", current.UserFeatureID)
		}
		current.Set = processed

		if slices.ContainsFunc(acc, func(n *FNode) bool { return nodesEqual(n, current) }) {
			continue
		}

		if len(processed.Features) > 0 {
			feat := processed.Features[0]

			current.FeatureIDAliases = aliasesFor(feat)

			for _, depID := range slices.Sorted(maps.Keys(feat.DependsOn)) {
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

			for _, iaID := range feat.InstallsAfter {
				dep := &FNode{
					Type:          "resolved",
					UserFeatureID: iaID,
					Options:       map[string]any{},
					DependsOn:     []*FNode{},
					InstallsAfter: []*FNode{},
				}
				softSet, err := processFeature(dep)
				if err != nil {
					return nil, err
				}
				if softSet == nil {
					return nil, unprocessedFeature("installsAfter dependency '%s' of Feature '%s' could not be processed.", iaID, current.UserFeatureID)
				}
				dep.Set = softSet
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

func applyOverrideFeatureInstallOrder(
	logger log.Logger,
	processFeature ProcessFeature,
	worklist []*FNode,
	overrideOrder []string,
) error {
	originalLength := len(overrideOrder)
	for i := originalLength - 1; i >= 0; i-- {
		overrideID := overrideOrder[i]
		roundPriority := originalLength - i

		override := &FNode{
			Type:          "override",
			UserFeatureID: overrideID,
			Options:       map[string]any{},
			RoundPriority: roundPriority,
			DependsOn:     []*FNode{},
			InstallsAfter: []*FNode{},
		}
		processed, err := processFeature(override)
		if err != nil {
			return err
		}
		if processed == nil {
			return unprocessedFeature("Feature '%s' in 'overrideFeatureInstallOrder' could not be processed.", overrideID)
		}
		override.Set = processed
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

func aliasesFor(feat Feature) []string {
	aliases := []string{}
	if feat.ID != "" {
		aliases = append(aliases, feat.ID)
	}
	return append(aliases, feat.LegacyIds...)
}
