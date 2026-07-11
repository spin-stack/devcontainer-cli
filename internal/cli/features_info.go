package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

var validInfoModes = map[string]struct{}{
	"manifest": {}, "tags": {}, "dependencies": {}, "verbose": {},
}

func realFeaturesInfoCmd() *cobra.Command {
	var logLevel, outputFormat string

	cmd := &cobra.Command{
		Use:   "info [mode] [feature]",
		Short: "Fetch metadata for a published Feature",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := args[0]
			if _, ok := validInfoModes[mode]; !ok {
				return fmt.Errorf("Invalid mode %q. Choose from: manifest, tags, dependencies, verbose", mode)
			}
			featureID := args[1]
			return runFeaturesInfo(mode, featureID, logLevel, outputFormat)
		},
	}

	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")
	cmd.Flags().StringVar(&outputFormat, "output-format", "text", "Output format.")

	return cmd
}

func runFeaturesInfo(mode, featureID, logLevel, outputFormat string) error {
	for _, v := range []struct {
		flag, val string
		choices   []string
	}{
		{"log-level", logLevel, []string{"info", "debug", "trace"}},
		{"output-format", outputFormat, []string{"text", "json"}},
	} {
		if err := validateEnum(v.flag, v.val, v.choices); err != nil {
			return err
		}
	}

	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevel),
		Format: "text",
		Writer: os.Stderr,
	})

	ref, err := oci.ParseRef(featureID)
	if err != nil {
		if outputFormat == "json" {
			fmt.Fprintln(os.Stdout, "{}")
		}
		return fmt.Errorf("Failed to parse Feature identifier %q", featureID)
	}

	env := osEnvMap()
	client := oci.NewClient(logger, env)

	jsonOutput := make(map[string]interface{})

	// Manifest
	if mode == "manifest" || mode == "verbose" {
		manifest, err := client.FetchManifest(ref, "")
		if err != nil {
			if outputFormat == "json" {
				fmt.Fprintln(os.Stdout, "{}")
			}
			return fmt.Errorf("No manifest found. If authentication is required, please login.")
		}

		if outputFormat == "text" {
			fmt.Println(encloseStringInBox("Manifest"))
			// Re-indent the raw manifest bytes so the key order matches the fetched
			// document (TS prints the object as-is); MarshalIndent of the struct/map
			// would sort keys and reorder annotations.
			var buf bytes.Buffer
			if json.Indent(&buf, manifest.ManifestBytes, "", "  ") == nil {
				fmt.Println(buf.String())
			} else {
				data, _ := json.MarshalIndent(manifest.Manifest, "", "  ")
				fmt.Println(string(data))
			}
			fmt.Println()
			fmt.Println(encloseStringInBox("Canonical Identifier"))
			fmt.Printf("%s\n\n", manifest.CanonicalID)
		} else {
			jsonOutput["manifest"] = manifest.Manifest
			jsonOutput["canonicalId"] = manifest.CanonicalID
		}
	}

	// Dependencies — text mode only, matching TS behavior.
	// Builds a recursive dependency graph by fetching each dependsOn feature's metadata.
	if (mode == "dependencies" || mode == "verbose") && outputFormat == "text" {
		logger.Write(fmt.Sprintf("Building dependency graph for '%s'...", featureID), log.LevelInfo)

		type depNode struct {
			id            string
			index         int
			aliases       []string
			dependsOn     []string
			installsAfter []string
		}

		// BFS: resolve dependsOn recursively, installsAfter only at each level.
		visited := map[string]bool{}
		var allNodes []depNode
		queue := []string{featureID}
		idx := 0

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if visited[current] {
				continue
			}
			visited[current] = true

			curRef, refErr := oci.ParseRef(current)
			if refErr != nil {
				continue
			}
			manifest, fetchErr := client.FetchManifest(curRef, "")
			if fetchErr != nil {
				continue
			}

			var meta features.Feature
			if manifest.Manifest.Annotations != nil {
				if metaJSON, ok := manifest.Manifest.Annotations["dev.containers.metadata"]; ok {
					json.Unmarshal([]byte(metaJSON), &meta)
				}
			}

			node := depNode{id: current, index: idx}
			if meta.ID != "" {
				node.aliases = append(node.aliases, meta.ID)
			}
			node.aliases = append(node.aliases, meta.LegacyIds...)
			for depID := range meta.DependsOn {
				node.dependsOn = append(node.dependsOn, depID)
				if !visited[depID] {
					queue = append(queue, depID)
				}
			}
			node.installsAfter = meta.InstallsAfter
			allNodes = append(allNodes, node)
			idx++
		}

		title := "Dependency Tree (Render with https://mermaid.live/)"
		fmt.Printf("┌%s┐\n", strings.Repeat("─", len(title)))
		fmt.Printf("│\033[1m%s\033[22m│\n", title)
		fmt.Printf("└%s┘\n", strings.Repeat("─", len(title)))

		// Mermaid flowchart matching TS generateMermaidDiagram format.
		// Node hashes use JSON.stringify of the full node (matching TS crypto hash).
		// installsAfter edges only render if the target is in the worklist.
		var sb strings.Builder
		sb.WriteString("flowchart\n")
		visitedIDs := map[string]bool{}
		for _, n := range allNodes {
			visitedIDs[n.id] = true
		}
		for _, n := range allNodes {
			nodeJSON, _ := json.Marshal(map[string]interface{}{
				"type": "user-provided", "userFeatureId": n.id,
				"options": map[string]interface{}{}, "roundPriority": n.index,
				"featureIdAliases": n.aliases,
			})
			nID := mermaidHash(nodeJSON)
			aliasStr := ""
			if len(n.aliases) > 0 {
				aliasStr = fmt.Sprintf("<br>aliases: %s", strings.Join(n.aliases, ", "))
			}
			fmt.Fprintf(&sb, "%s[%s<br/><%d>%s]\n", nID, n.id, n.index, aliasStr)
			for _, depID := range n.dependsOn {
				depJSON, _ := json.Marshal(map[string]interface{}{"userFeatureId": depID})
				fmt.Fprintf(&sb, "%s --> %s\n", nID, mermaidHash(depJSON))
			}
			// Only render installsAfter edges if target is in worklist
			for _, afterID := range n.installsAfter {
				if visitedIDs[afterID] {
					afterJSON, _ := json.Marshal(map[string]interface{}{"userFeatureId": afterID})
					fmt.Fprintf(&sb, "%s -.-> %s\n", nID, mermaidHash(afterJSON))
				}
			}
		}
		fmt.Println(sb.String())
	}

	// Tags
	if mode == "tags" || mode == "verbose" {
		tags, err := client.GetPublishedTags(ref)
		if err != nil || len(tags) == 0 {
			if outputFormat == "json" {
				fmt.Fprintln(os.Stdout, "{}")
			}
			return fmt.Errorf("No published versions found for feature %q", ref.Resource)
		}

		if outputFormat == "text" {
			fmt.Println(encloseStringInBox("Published Tags"))
			// TS joins with "\n   ": the first tag is flush, the rest indented by 3.
			fmt.Println(strings.Join(tags, "\n   "))
		} else {
			jsonOutput["publishedTags"] = tags
		}
	}

	if outputFormat == "json" {
		data, _ := json.MarshalIndent(jsonOutput, "", "    ")
		fmt.Fprintln(os.Stdout, string(data))
	}

	return nil
}

// encloseStringInBox renders a single-line title inside a box sized exactly to the
// title (matching the TS encloseStringInBox): the ANSI bold codes are not counted
// toward the width.
func encloseStringInBox(str string) string {
	w := len([]rune(str))
	bar := strings.Repeat("─", w)
	return "┌" + bar + "┐\n" +
		"│\033[1m" + str + "\033[22m│\n" +
		"└" + bar + "┘"
}

// mermaidHash generates a short hex hash for Mermaid nodes (matches TS crypto.createHash('sha256').update(JSON.stringify(node))).
func mermaidHash(jsonData []byte) string {
	h := sha256.Sum256(jsonData)
	return fmt.Sprintf("%x", h[:3])
}
