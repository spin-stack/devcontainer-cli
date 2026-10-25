package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/imagemeta"
)

// Prefixes of the compose override files the TS CLI writes to the persisted
// folder (dockerCompose.ts startContainer). They are recorded in the container's
// com.docker.compose.project.config_files label and restored by prefix.
const (
	composeBuildOverridePrefix    = "docker-compose.devcontainer.build"
	composeFeaturesOverridePrefix = "docker-compose.devcontainer.containerFeatures"
)

// composeVersionLineRe is TS readVersionPrefix's /^\s*(version:.*)$/m.
var composeVersionLineRe = regexp.MustCompile(`(?m)^\s*(version:.*)$`)

// readComposeVersionPrefix returns the first compose file's `version:` line
// followed by a blank line, or "" (TS readVersionPrefix).
func readComposeVersionPrefix(composeFiles []string) (string, error) {
	if len(composeFiles) == 0 {
		return "", nil
	}
	data, err := os.ReadFile(composeFiles[0])
	if err != nil {
		return "", fmt.Errorf("read compose file: %w", err)
	}
	m := composeVersionLineRe.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[1]) + "\n\n", nil
}

// composeMount is the subset of the TS Mount the compose override uses.
type composeMount struct {
	Type     string
	Source   string
	Target   string
	External bool
	// ReadOnly renders the volume with ":ro". TS convertMountToVolume drops
	// readonly; Go keeps it (upstream #881, see TestMetadataMounts_ReadonlyObject).
	ReadOnly bool
}

// composeVolumeSpec is TS convertMountToVolume ("[source:]target"), plus ":ro"
// for a read-only mount (upstream #881).
func composeVolumeSpec(m composeMount) string {
	volume := m.Target
	if m.Source != "" {
		volume = m.Source + ":" + m.Target
	}
	if m.ReadOnly {
		volume += ":ro"
	}
	return volume
}

// composeMountsFromMetadata converts merged-config mounts (strings or objects)
// and --mount values the way TS generateFeaturesComposeOverrideContent does:
// strings go through parseMount (`key=value` pairs, src/dst/destination
// normalized), objects are taken as they are. ${devcontainerId} was already
// substituted by resolve.
func composeMountsFromMetadata(entries []any, resolve func(string) (string, error)) ([]composeMount, error) {
	mounts := make([]composeMount, 0, len(entries))
	for _, entry := range entries {
		var m composeMount
		switch raw := entry.(type) {
		case string:
			spec, err := resolve(raw)
			if err != nil {
				return nil, err
			}
			m = parseComposeMountString(spec)
		case map[string]any:
			str := func(k string) (string, error) {
				v, _ := raw[k].(string)
				return resolve(v)
			}
			var err error
			m.Type, _ = raw["type"].(string)
			if m.Source, err = str("source"); err != nil {
				return nil, err
			}
			if m.Target, err = str("target"); err != nil {
				return nil, err
			}
			m.External, _ = raw["external"].(bool)
			m.ReadOnly, _ = raw["readonly"].(bool)
		default:
			return nil, fmt.Errorf("unsupported mount metadata type %T", entry)
		}
		mounts = append(mounts, m)
	}
	return mounts, nil
}

// parseComposeMountString is TS parseMount: comma-separated key=value pairs with
// src → source and dst/destination → target.
func parseComposeMountString(spec string) composeMount {
	var m composeMount
	for part := range strings.SplitSeq(spec, ",") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "type":
			m.Type = value
		case "source", "src":
			m.Source = value
		case "target", "dst", "destination":
			m.Target = value
		case "external":
			m.External = value == "true"
		case "readonly", "ro":
			m.ReadOnly = value == "" || value == "true" || value == "1"
		}
	}
	return m
}

// composeFeaturesOverride holds the inputs of TS generateFeaturesComposeOverrideContent.
type composeFeaturesOverride struct {
	VersionPrefix     string
	Service           string
	UpdatedImageName  string
	OriginalImageName string
	Merged            *imagemeta.MergedConfig
	// ServiceEntrypoint/ServiceCommand are the compose service's entrypoint and
	// command (nil when the service does not set them).
	ServiceEntrypoint []string
	ServiceCommand    []string
	// ImageEntrypoint/ImageCmd are the image's Config.Entrypoint/Cmd.
	ImageEntrypoint []string
	ImageCmd        []string
	Labels          []string
	Mounts          []composeMount
	GPU             bool
}

// jsonString is JSON.stringify for a string (no HTML escaping).
func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// escapeComposeDollar is TS's `.replace(/\$/g, '$$$$')`: $ → $$ so compose does
// not interpolate it.
func escapeComposeDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// generateComposeFeaturesOverride renders the YAML of the compose override that
// configures the dev container service, byte for byte like TS
// generateFeaturesComposeOverrideContent (dockerCompose.ts). The entrypoint is a
// multi-line double-quoted YAML scalar on purpose: YAML folds each line break
// into a space, which is the exact script the TS CLI runs.
func generateComposeFeaturesOverride(in composeFeaturesOverride) string {
	merged := in.Merged
	if merged == nil {
		merged = &imagemeta.MergedConfig{}
	}
	overrideCommand := merged.OverrideCommand != nil && *merged.OverrideCommand

	userEntrypoint := []string{}
	userCommand := []string{}
	if !overrideCommand {
		if in.ServiceEntrypoint != nil {
			userEntrypoint = in.ServiceEntrypoint // $ already escaped.
		} else {
			for _, c := range in.ImageEntrypoint {
				userEntrypoint = append(userEntrypoint, escapeComposeDollar(c))
			}
		}
		switch {
		case in.ServiceCommand != nil:
			userCommand = in.ServiceCommand // $ already escaped.
		case in.ServiceEntrypoint != nil:
			// Ignore image CMD per docker-compose.yml spec.
		default:
			for _, c := range in.ImageCmd {
				userCommand = append(userCommand, escapeComposeDollar(c))
			}
		}
	}
	// TS emits `command:` unless userCommand is the compose command itself.
	emitCommand := overrideCommand || in.ServiceCommand == nil

	var b strings.Builder
	b.WriteString(in.VersionPrefix)
	b.WriteString("services:\n")
	fmt.Fprintf(&b, "  '%s':", in.Service)
	if in.UpdatedImageName != in.OriginalImageName {
		fmt.Fprintf(&b, "\n    image: %s", in.UpdatedImageName)
	}
	b.WriteString("\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\n")
	b.WriteString("trap \\\"exit 0\\\" 15\\n\n")
	b.WriteString(strings.Join(merged.Entrypoints, "\\n\n"))
	b.WriteString("\\n\n")
	b.WriteString("exec \\\"$$@\\\"\\n\n")
	b.WriteString("while sleep 1 & wait $$!; do :; done\", \"-\"")
	for _, a := range userEntrypoint {
		b.WriteString(", " + jsonString(a))
	}
	b.WriteString("]")
	if emitCommand {
		b.WriteString("\n    command: " + jsonString(userCommand))
	}
	if merged.Init != nil && *merged.Init {
		b.WriteString("\n    init: true")
	}
	if merged.ContainerUser != "" {
		b.WriteString("\n    user: " + merged.ContainerUser)
	}
	if len(merged.ContainerEnv) > 0 {
		b.WriteString("\n    environment:")
		keys := make([]string, 0, len(merged.ContainerEnv))
		for k := range merged.ContainerEnv {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			v := strings.ReplaceAll(merged.ContainerEnv[k], "\n", `\n`)
			v = escapeComposeDollar(v)
			v = strings.ReplaceAll(v, "'", "''")
			fmt.Fprintf(&b, "\n      - '%s=%s'", k, v)
		}
	}
	if merged.Privileged != nil && *merged.Privileged {
		b.WriteString("\n    privileged: true")
	}
	if len(merged.CapAdd) > 0 {
		b.WriteString("\n    cap_add:")
		for _, c := range merged.CapAdd {
			b.WriteString("\n      - " + c)
		}
	}
	if len(merged.SecurityOpt) > 0 {
		b.WriteString("\n    security_opt:")
		for _, s := range merged.SecurityOpt {
			b.WriteString("\n      - " + s)
		}
	}
	if len(in.Labels) > 0 {
		b.WriteString("\n    labels:")
		for _, l := range in.Labels {
			fmt.Fprintf(&b, "\n      - '%s'", strings.ReplaceAll(escapeComposeDollar(l), "'", "''"))
		}
	}
	if len(in.Mounts) > 0 {
		b.WriteString("\n    volumes:")
		for _, m := range in.Mounts {
			b.WriteString("\n      - " + composeVolumeSpec(m))
		}
	}
	if in.GPU {
		b.WriteString("\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]")
	}
	var named []composeMount
	for _, m := range in.Mounts {
		if m.Type == "volume" && m.Source != "" {
			named = append(named, m)
		}
	}
	if len(named) > 0 {
		b.WriteString("\nvolumes:")
		for _, m := range named {
			fmt.Fprintf(&b, "\n  \n  %s:", m.Source)
			if m.External {
				b.WriteString("\n    external: true")
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

// composeBuildOverride holds the inputs of the build override TS
// buildAndExtendDockerCompose writes.
type composeBuildOverride struct {
	VersionPrefix string
	Service       string
	// Image is the `image:` set for an image-only service ("" for none).
	Image      string
	Dockerfile string
	// Target is the overridden build target ("" when the service sets none).
	Target string
	// Context is the build context set when the service has none ("" for none).
	Context             string
	BuildKitInlineCache bool
	BuildArgs           [][2]string
	AdditionalContexts  [][2]string
	CacheFrom           []string
}

// buildContent is TS buildOverrideContent; empty when there is no feature build.
func (in composeBuildOverride) buildContent() string {
	if in.Dockerfile == "" {
		return ""
	}
	var b strings.Builder
	if in.Image != "" {
		fmt.Fprintf(&b, "    image: %s\n", in.Image)
	}
	b.WriteString("    build:\n")
	fmt.Fprintf(&b, "      dockerfile: %s\n", in.Dockerfile)
	if in.Target != "" {
		fmt.Fprintf(&b, "      target: %s\n", in.Target)
	}
	if in.Context != "" {
		fmt.Fprintf(&b, "      context: %s\n", in.Context)
	}
	if len(in.BuildArgs) > 0 || in.BuildKitInlineCache {
		b.WriteString("      args:\n")
		if in.BuildKitInlineCache {
			b.WriteString("        - BUILDKIT_INLINE_CACHE=1\n")
		}
		for _, a := range in.BuildArgs {
			fmt.Fprintf(&b, "        - %s=%s\n", a[0], a[1])
		}
	}
	if len(in.AdditionalContexts) > 0 {
		b.WriteString("      additional_contexts:\n")
		for _, c := range in.AdditionalContexts {
			fmt.Fprintf(&b, "        - %s=%s\n", c[0], c[1])
		}
	}
	return b.String()
}

// generateComposeBuildOverride renders the build override YAML like TS
// buildAndExtendDockerCompose, or "" when TS writes no build override (no
// feature build and no --cache-from).
func generateComposeBuildOverride(in composeBuildOverride) string {
	content := in.buildContent()
	if content == "" && len(in.CacheFrom) == 0 {
		return ""
	}
	cacheFrom := ""
	if len(in.CacheFrom) > 0 {
		items := make([]string, len(in.CacheFrom))
		for i, c := range in.CacheFrom {
			items[i] = "        - " + c + "\n"
		}
		cacheFrom = "      cache_from:\n" + strings.Join(items, "\n")
	}
	return in.VersionPrefix + "services:\n  " + in.Service + ":\n" + strings.TrimRight(content, " \t\r\n") + "\n" + cacheFrom + "\n"
}
