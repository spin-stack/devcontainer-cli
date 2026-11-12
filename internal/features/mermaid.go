package features

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// Node hashes are internally consistent (an edge points at the target node's own
// hash) but need not match the TS hash byte-for-byte: the parity harness scrubs
// them. roundPriority is emitted as TS does, so overrideFeatureInstallOrder is
// reflected in the diagram.
func GenerateMermaidDiagram(graph []*FNode) string {
	var sb strings.Builder
	sb.WriteString("flowchart\n")
	for _, node := range graph {
		if node.Type != "user-provided" {
			continue
		}
		sb.WriteString(generateMermaidNode(node))
		sb.WriteString("\n")
		mermaidSubtree(node, graph, &sb, map[*FNode]bool{})
	}
	return sb.String()
}

func mermaidSubtree(current *FNode, worklist []*FNode, sb *strings.Builder, onPath map[*FNode]bool) {
	if onPath[current] {
		return
	}
	onPath[current] = true
	defer delete(onPath, current)

	for _, child := range current.DependsOn {
		for _, w := range worklist {
			if nodesEqual(w, child) {
				fmt.Fprintf(sb, "%s --> %s\n", generateMermaidNode(current), generateMermaidNode(w))
			}
		}
		mermaidSubtree(child, worklist, sb, onPath)
	}
	for _, softDep := range current.InstallsAfter {
		for _, w := range worklist {
			if nodeSatisfiesSoftDep(w, softDep) {
				fmt.Fprintf(sb, "%s -.-> %s\n", generateMermaidNode(current), generateMermaidNode(w))
			}
		}
		mermaidSubtree(softDep, worklist, sb, onPath)
	}
}

func generateMermaidNode(node *FNode) string {
	hash := mermaidNodeHash(node)
	aliases := ""
	if len(node.FeatureIDAliases) > 0 {
		aliases = "<br>aliases: " + strings.Join(node.FeatureIDAliases, ", ")
	}
	return fmt.Sprintf("%s[%s<br/><%d>%s]", hash, node.UserFeatureID, node.RoundPriority, aliases)
}

func mermaidNodeHash(node *FNode) string {
	options := node.Options
	if options == nil {
		options = map[string]any{}
	}
	payload, _ := json.Marshal(map[string]any{
		"type":             node.Type,
		"userFeatureId":    node.UserFeatureID,
		"options":          options,
		"roundPriority":    node.RoundPriority,
		"featureIdAliases": node.FeatureIDAliases,
	})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:3])
}
