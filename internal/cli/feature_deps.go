package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

// renderDependencyMermaid builds the feature dependency graph for the given root
// feature ids (BFS over each feature's dependsOn, plus installsAfter edges that
// land on a worklist member) and renders it in the TS generateMermaidDiagram
// format. roundPriority is 0 for every node — the TS CLI only assigns a non-zero
// priority when overrideFeatureInstallOrder is set. Node hashes are internally
// consistent (an edge points at the target node's own hash); they need not match
// the TS hash byte-for-byte because the parity harness scrubs them.
func renderDependencyMermaid(client *oci.Client, logger log.Log, roots []string) string {
	type depNode struct {
		id            string
		aliases       []string
		dependsOn     []string
		installsAfter []string
	}

	visited := map[string]bool{}
	var nodes []depNode
	queue := append([]string{}, roots...)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		logger.Write(fmt.Sprintf("Resolving Feature dependencies for '%s'...", current), log.LevelInfo)

		ref, err := oci.ParseRef(current)
		if err != nil {
			continue
		}
		manifest, err := client.FetchManifest(ref, "")
		if err != nil || manifest.Manifest == nil {
			continue
		}
		var meta features.Feature
		if mj, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok {
			json.Unmarshal([]byte(mj), &meta)
		}

		node := depNode{id: current}
		// The first alias is the feature's own id. TS reads it from the feature
		// metadata; when a feature's manifest omits the dev.containers.metadata
		// annotation, fall back to the ref's feature name (last path segment), which
		// is the same value.
		featureID := meta.ID
		if featureID == "" {
			featureID = ref.ID
		}
		if featureID != "" {
			node.aliases = append(node.aliases, featureID)
		}
		node.aliases = append(node.aliases, meta.LegacyIds...)
		for depID := range meta.DependsOn {
			node.dependsOn = append(node.dependsOn, depID)
			if !visited[depID] {
				queue = append(queue, depID)
			}
		}
		sort.Strings(node.dependsOn)
		node.installsAfter = meta.InstallsAfter
		nodes = append(nodes, node)
	}

	inWorklist := map[string]bool{}
	aliasByID := map[string][]string{}
	for _, n := range nodes {
		inWorklist[n.id] = true
		aliasByID[n.id] = n.aliases
	}

	hashOf := func(id string, aliases []string) string {
		b, _ := json.Marshal(map[string]interface{}{
			"type":             "user-provided",
			"userFeatureId":    id,
			"options":          map[string]interface{}{},
			"roundPriority":    0,
			"featureIdAliases": aliases,
		})
		return mermaidHash(b)
	}

	var sb strings.Builder
	sb.WriteString("flowchart\n")
	for _, n := range nodes {
		nID := hashOf(n.id, n.aliases)
		aliasStr := ""
		if len(n.aliases) > 0 {
			aliasStr = fmt.Sprintf("<br>aliases: %s", strings.Join(n.aliases, ", "))
		}
		fmt.Fprintf(&sb, "%s[%s<br/><0>%s]\n", nID, n.id, aliasStr)
		for _, depID := range n.dependsOn {
			fmt.Fprintf(&sb, "%s --> %s\n", nID, hashOf(depID, aliasByID[depID]))
		}
		for _, afterID := range n.installsAfter {
			if inWorklist[afterID] {
				fmt.Fprintf(&sb, "%s -.-> %s\n", nID, hashOf(afterID, aliasByID[afterID]))
			}
		}
	}
	return sb.String()
}
