package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
)

const (
	composeBuildOverridePrefix    = "docker-compose.devcontainer.build"
	composeFeaturesOverridePrefix = "docker-compose.devcontainer.containerFeatures"
)

// composeVersionLineRe is TS readVersionPrefix's /^\s*(version:.*)$/m with
// JavaScript's semantics: a line also starts after \r, \u2028 or \u2029, \s
// includes \v and the Unicode spaces, and . stops at any line terminator (a
// CRLF file's version line has no trailing \r).
var composeVersionLineRe = regexp.MustCompile(`(?m)(?:^|[\r\x{2028}\x{2029}])[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*(version:[^\n\r\x{2028}\x{2029}]*)`)

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

type composeMount struct {
	Type     string
	Source   string
	Target   string
	External bool
	ReadOnly bool
}

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

func composeTopLevelVolume(m composeMount) string {
	volume := "\n  " + m.Source + ":"
	if m.External {
		volume += "\n    external: true"
	}
	return volume
}

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

func parseComposeMountString(spec string) composeMount {
	var m composeMount
	for part := range strings.SplitSeq(spec, ",") {
		// TS splits on every "=" and keeps the second piece only.
		key, value, _ := strings.Cut(part, "=")
		value, _, _ = strings.Cut(value, "=")
		switch key {
		case "type":
			m.Type = value
		case "source", "src":
			m.Source = value
		case "target", "dst", "destination":
			m.Target = value
		case "external":
			// TS tests the string's truthiness: "false" is external too.
			m.External = value != ""
		case "readonly", "ro":
			m.ReadOnly = value == "" || value == "true" || value == "1"
		}
	}
	return m
}

type composeFeaturesOverride struct {
	VersionPrefix     string
	Service           string
	UpdatedImageName  string
	OriginalImageName string
	Merged            *imagemeta.MergedConfig
	ServiceEntrypoint []string
	ServiceCommand    []string
	ImageEntrypoint   []string
	ImageCmd          []string
	Labels            []string
	Mounts            []composeMount
	GPU               bool
}

func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

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
			userEntrypoint = in.ServiceEntrypoint
		} else {
			for _, c := range in.ImageEntrypoint {
				userEntrypoint = append(userEntrypoint, escapeComposeDollar(c))
			}
		}
		switch {
		case in.ServiceCommand != nil:
			userCommand = in.ServiceCommand
		case in.ServiceEntrypoint != nil:
			// Ignore image CMD per docker-compose.yml spec.
		default:
			for _, c := range in.ImageCmd {
				userCommand = append(userCommand, escapeComposeDollar(c))
			}
		}
	}
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
		for _, k := range features.PropertyOrder(merged.ContainerEnv, merged.ContainerEnvOrder) {
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
			b.WriteString("\n  " + composeTopLevelVolume(m))
		}
	}
	b.WriteString("\n")
	return b.String()
}

type composeBuildOverride struct {
	VersionPrefix       string
	Service             string
	Image               string
	Dockerfile          string
	Target              string
	Context             string
	BuildKitInlineCache bool
	BuildArgs           [][2]string
	AdditionalContexts  [][2]string
	CacheFrom           []string
}

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
