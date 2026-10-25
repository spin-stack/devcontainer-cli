package imagemeta

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/devcontainers/cli/internal/features"
)

// FeaturesContainerTempDestFolder is where the feature-install layers stage each
// Feature inside the image (TS FEATURES_CONTAINER_TEMP_DEST_FOLDER).
const FeaturesContainerTempDestFolder = "/tmp/dev-container-features"

// FeaturesBuiltinEnvFile is the file, at the root of the features folder, that
// holds the _CONTAINER_USER/_REMOTE_USER builtins every Feature's install
// wrapper sources (TS getFeaturesBuildOptions).
const FeaturesBuiltinEnvFile = "devcontainer-features.builtin.env"

// FeatureEnvFile and FeatureInstallWrapperFile are the files written into each
// Feature's folder (TS getFeaturesBuildOptions, internalVersion 2).
const (
	FeatureEnvFile            = "devcontainer-features.env"
	FeatureInstallWrapperFile = "devcontainer-features-install.sh"
)

// ExtendImageBuildInfo holds the Dockerfile and build context needed
// to extend a base image with features.
type ExtendImageBuildInfo struct {
	DockerfilePrefixContent string
	DockerfileContent       string
	OverrideTarget          string
	BuildArgs               map[string]string
	// BuildKitContexts maps an additional build context name to its folder; "."
	// stands for the features folder (TS dstFolder), which the caller resolves.
	BuildKitContexts map[string]string
	DstFolder        string
}

// FeaturesBuildParams are the inputs of GenerateFeaturesBuildOptions (TS
// getFeaturesBuildOptions).
type FeaturesBuildParams struct {
	// BaseName is the _DEV_CONTAINERS_BASE_IMAGE build argument: the image (or
	// the compose service's Dockerfile stage) the Features are installed on.
	BaseName    string
	FeatureSets []*features.Set
	Metadata    []Entry
	// ContainerUser and RemoteUser are TS findContainerUsers' result; they seed
	// the _CONTAINER_USER_HOME/_REMOTE_USER_HOME builtins.
	ContainerUser, RemoteUser string
	// ImageUser is the image's own user (TS imageBuildInfo.user), restored after
	// the Features are installed through the _DEV_CONTAINERS_IMAGE_USER argument.
	ImageUser string
	// UseBuildKitContexts copies the Features' content from the
	// dev_containers_feature_content_source additional build context; otherwise
	// from the ComposeFeatureContentImage image's /tmp/build-features/.
	UseBuildKitContexts bool
	// Syntax is the `# syntax=` value the prefix declares ("" for none); the
	// caller applies TS's rules (omitSyntaxDirective, Docker >= 23, build
	// contexts support of the user's Dockerfile).
	Syntax string
	// ConfigContainerEnv is devcontainer.json's containerEnv, emitted as ENV
	// lines after the metadata label (TS #{containerEnvMetadata}).
	ConfigContainerEnv map[string]string
	// SecretIDs are Docker build-secret ids mounted into every feature-install
	// RUN as `--mount=type=secret,id=<id>`, so a Feature's install.sh can read
	// /run/secrets/<id> (#1078). TS has no equivalent; empty when no build
	// secrets are in play.
	SecretIDs []string
}

// ComposeFeatureContentImage is the temporary image the TS CLI builds to hold the
// Features' content when the build cannot use an additional build context
// (containerFeatures.ts getFeaturesBuildOptions buildContentImageName).
const ComposeFeatureContentImage = "dev_container_feature_content_temp"

// GenerateComposeLabelBuild creates the Dockerfile content that the TS
// buildAndExtendDockerCompose appends to a compose service's Dockerfile when
// there are no Features: only the metadata label (TS getImageBuildOptions). The
// global `ARG _DEV_CONTAINERS_BASE_IMAGE` is part of DockerfilePrefixContent,
// because a FROM can only use an ARG declared before the first FROM of the
// combined Dockerfile. syntax is the `# syntax=` value to declare ("" for none).
func GenerateComposeLabelBuild(baseName string, metadata []Entry, syntax string) *ExtendImageBuildInfo {
	prefix := "\n    ARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n"
	if syntax != "" {
		prefix = "# syntax=" + syntax + prefix
	}
	return &ExtendImageBuildInfo{
		DockerfilePrefixContent: prefix,
		DockerfileContent:       "\nFROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage\n" + devcontainerMetadataLabel(metadata) + "\n",
		OverrideTarget:          "dev_containers_target_stage",
		BuildArgs:               map[string]string{"_DEV_CONTAINERS_BASE_IMAGE": baseName},
		BuildKitContexts:        map[string]string{},
	}
}

// containerFeaturesBaseDockerfile is TS getContainerFeaturesBaseDockerFile.
const containerFeaturesBaseDockerfile = `

#{nonBuildKitFeatureContentFallback}

ARG _DEV_CONTAINERS_BASE_IMAGE=scratch

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_feature_content_normalize
USER root
COPY --from=dev_containers_feature_content_source #{builtinEnvSource} /tmp/build-features/
RUN chmod -R 0755 /tmp/build-features/

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage

USER root

RUN mkdir -p ` + FeaturesContainerTempDestFolder + `
COPY --from=dev_containers_feature_content_normalize /tmp/build-features/ ` + FeaturesContainerTempDestFolder + `

#{featureLayer}

#{containerEnv}

ARG _DEV_CONTAINERS_IMAGE_USER=root
USER $_DEV_CONTAINERS_IMAGE_USER

#{devcontainerMetadata}

#{containerEnvMetadata}
`

// GenerateFeaturesBuildOptions generates the Dockerfile that installs the
// Features, as TS getFeaturesBuildOptions does with
// getContainerFeaturesBaseDockerFile and getFeatureLayers: a normalize stage
// copies the builtin env file, then each Feature's folder (named by its
// consecutiveId) is copied or bind-mounted into
// /tmp/dev-container-features/<consecutiveId> and its
// devcontainer-features-install.sh wrapper runs, preceded by the Feature's
// containerEnv as ENV lines. The files the Dockerfile refers to are written by
// the caller (FeatureEnvVariables, FeatureInstallWrapperScript,
// FeaturesBuiltinEnvContent). Only internalVersion 2 Features exist here: the
// v1 GitHub Release Features are not supported (docs/DIVERGENCES.md).
func GenerateFeaturesBuildOptions(p FeaturesBuildParams) *ExtendImageBuildInfo {
	contentSourceRootPath := "/tmp/build-features/"
	fallback := "FROM " + ComposeFeatureContentImage + " as dev_containers_feature_content_source"
	if p.UseBuildKitContexts {
		contentSourceRootPath = "."
		fallback = ""
	}
	replacer := strings.NewReplacer(
		"#{nonBuildKitFeatureContentFallback}", fallback,
		"#{builtinEnvSource}", path.Join(contentSourceRootPath, FeaturesBuiltinEnvFile),
		"#{featureLayer}", featureLayers(p, contentSourceRootPath),
		// TS generateContainerEnvsV1: only v1 Features contribute here.
		"#{containerEnv}", "",
		"#{devcontainerMetadata}", devcontainerMetadataLabel(p.Metadata),
		"#{containerEnvMetadata}", generateContainerEnvs(p.ConfigContainerEnv, true),
	)
	dockerfile := replacer.Replace(containerFeaturesBaseDockerfile)

	prefix := "\nARG _DEV_CONTAINERS_BASE_IMAGE=scratch\n"
	if p.Syntax != "" {
		prefix = "# syntax=" + p.Syntax + prefix
	}
	imageUser := p.ImageUser
	if imageUser == "" {
		imageUser = "root"
	}
	buildKitContexts := map[string]string{}
	if p.UseBuildKitContexts {
		buildKitContexts["dev_containers_feature_content_source"] = "."
	}
	return &ExtendImageBuildInfo{
		DockerfilePrefixContent: prefix,
		DockerfileContent:       dockerfile,
		OverrideTarget:          "dev_containers_target_stage",
		BuildArgs: map[string]string{
			"_DEV_CONTAINERS_BASE_IMAGE":             p.BaseName,
			"_DEV_CONTAINERS_IMAGE_USER":             imageUser,
			"_DEV_CONTAINERS_FEATURE_CONTENT_SOURCE": ComposeFeatureContentImage,
		},
		BuildKitContexts: buildKitContexts,
	}
}

// featureLayers is TS getFeatureLayers for internalVersion 2 Features.
func featureLayers(p FeaturesBuildParams, contentSourceRootPath string) string {
	builtinsEnvFile := path.Join(FeaturesContainerTempDestFolder, FeaturesBuiltinEnvFile)
	var b strings.Builder
	fmt.Fprintf(&b, "RUN \\\necho \"_CONTAINER_USER_HOME=$(%s | cut -d: -f6)\" >> %s && \\\necho \"_REMOTE_USER_HOME=$(%s | cut -d: -f6)\" >> %s\n\n",
		getEntPasswdShellCommand(p.ContainerUser), builtinsEnvFile, getEntPasswdShellCommand(p.RemoteUser), builtinsEnvFile)

	secretMounts := ""
	for _, id := range p.SecretIDs {
		secretMounts += " --mount=type=secret,id=" + id
	}
	for _, set := range p.FeatureSets {
		for _, feat := range set.Features {
			b.WriteString(generateContainerEnvs(feat.ContainerEnv, false))
			source := path.Join(contentSourceRootPath, feat.ConsecutiveId)
			dest := path.Join(FeaturesContainerTempDestFolder, feat.ConsecutiveId)
			if !p.UseBuildKitContexts {
				runMounts := ""
				if secretMounts != "" {
					runMounts = strings.TrimPrefix(secretMounts, " ") + " "
				}
				fmt.Fprintf(&b, "\nCOPY --chown=root:root --from=dev_containers_feature_content_source %s %s\nRUN %schmod -R 0755 %s \\\n&& cd %s \\\n&& chmod +x ./%s \\\n&& ./%s\n\n",
					source, dest, runMounts, dest, dest, FeatureInstallWrapperFile, FeatureInstallWrapperFile)
				continue
			}
			fmt.Fprintf(&b, "\nRUN --mount=type=bind,from=dev_containers_feature_content_source,source=%s,target=/tmp/build-features-src/%s%s \\\n    cp -ar /tmp/build-features-src/%s %s \\\n && chmod -R 0755 %s \\\n && cd %s \\\n && chmod +x ./%s \\\n && ./%s \\\n && rm -rf %s\n\n",
				source, feat.ConsecutiveId, secretMounts, feat.ConsecutiveId, FeaturesContainerTempDestFolder, dest, dest, FeatureInstallWrapperFile, FeatureInstallWrapperFile, dest)
		}
	}
	return b.String()
}

// generateContainerEnvs is TS generateContainerEnvs: one `ENV k="v"` line per
// entry, escaping double quotes and backslashes (and dollar signs when
// escapeDollar). Go maps carry no insertion order, so keys are sorted.
func generateContainerEnvs(env map[string]string, escapeDollar bool) string {
	if len(env) == 0 {
		return ""
	}
	special := `"\`
	if escapeDollar {
		special += "$"
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, "ENV "+k+`="`+backslashEscape(env[k], special)+`"`)
	}
	return strings.Join(lines, "\n")
}

// backslashEscape prefixes every character of s found in special with a
// backslash (TS .replace(/(?=[...])/g, '\\')).
func backslashEscape(s, special string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// devcontainerMetadataLabel is TS getDevcontainerMetadataLabel: the metadata
// label as a JSON array with one entry per continued line, "" without entries.
func devcontainerMetadataLabel(entries []Entry) string {
	entries = nonEmptyEntries(entries)
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		data, _ := json.Marshal(e)
		parts = append(parts, " \\\n"+backslashEscape(string(data), `"\$`))
	}
	return "LABEL " + MetadataLabel + `="[` + strings.Join(parts, ",") + ` \` + "\n]\""
}

// regExpSpecial are the characters TS escapeRegExCharacters escapes.
var regExpSpecial = regexp.MustCompile(`[.*+?^${}()|[\]\\]`)

// getEntPasswdShellCommand is TS getEntPasswdShellCommand: a shell expression
// printing the /etc/passwd line of a user name or uid, preferring getent and
// falling back to grep on getent-less images (alpine/busybox). The leading
// space keeps `$(` from forming an arithmetic expansion with the `(`.
func getEntPasswdShellCommand(userNameOrID string) string {
	escapedForShell := regexp.MustCompile(`['\\]`).ReplaceAllString(userNameOrID, `\$0`)
	escapedForRegExp := strings.ReplaceAll(regExpSpecial.ReplaceAllString(userNameOrID, `\$0`), "'", `\'`)
	return fmt.Sprintf(` (command -v getent >/dev/null 2>&1 && getent passwd '%s' || grep -E '^%s|^[^:]*:[^:]*:%s:' /etc/passwd || true)`, escapedForShell, escapedForRegExp, escapedForRegExp)
}

// FeaturesBuiltinEnvContent is the devcontainer-features.builtin.env content
// (TS getFeaturesBuildOptions builtinVariables).
func FeaturesBuiltinEnvContent(containerUser, remoteUser string) string {
	return "_CONTAINER_USER=" + containerUser + "\n_REMOTE_USER=" + remoteUser + "\n"
}

// FeatureFileInfo holds the devcontainer-feature.json properties TS reads from
// the staged file that features.Feature does not keep.
type FeatureFileInfo struct {
	// OptionOrder lists the keys of "options" in document order (a JS object
	// keeps insertion order; a Go map does not).
	OptionOrder []string
	Deprecated  bool
	CurrentID   string
}

// ParseFeatureFileInfo reads FeatureFileInfo from a devcontainer-feature.json
// document. A document that is not a JSON object yields an error.
func ParseFeatureFileInfo(data []byte) (FeatureFileInfo, error) {
	var info FeatureFileInfo
	var raw struct {
		Options    json.RawMessage `json:"options"`
		Deprecated bool            `json:"deprecated"`
		CurrentID  string          `json:"currentId"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return info, err
	}
	info.Deprecated, info.CurrentID = raw.Deprecated, raw.CurrentID
	if len(raw.Options) == 0 || string(raw.Options) == "null" {
		return info, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw.Options)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return info, nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return info, err
		}
		key, _ := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return info, err
		}
		if !slices.Contains(info.OptionOrder, key) {
			info.OptionOrder = append(info.OptionOrder, key)
		}
	}
	return info, nil
}

// FeatureEnvVariables is TS getFeatureEnvVariables for an internalVersion 2
// Feature: `<SAFE_OPTION>="<value>"` for each entry of the Feature's value
// object (option defaults overlaid with the user's values, TS
// getFeatureValueObject), unescaped as in TS.
func FeatureEnvVariables(feat features.Feature, info FeatureFileInfo) []string {
	keys, values := featureValueObject(feat, info)
	vars := make([]string, 0, len(keys))
	for _, k := range keys {
		vars = append(vars, safeID(k)+`="`+jsString(values[k])+`"`)
	}
	return vars
}

// featureValueObject is TS getFeatureValueObject, returning the keys in JS
// object order.
func featureValueObject(feat features.Feature, info FeatureFileInfo) ([]string, map[string]any) {
	order := append([]string(nil), info.OptionOrder...)
	var rest []string
	for k := range feat.Options {
		if !slices.Contains(order, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	var keys []string
	values := map[string]any{}
	set := func(k string, v any) {
		if _, ok := values[k]; !ok {
			keys = append(keys, k)
		}
		values[k] = v
	}
	for _, k := range order {
		opt, ok := feat.Options[k].(map[string]any)
		if !ok {
			continue
		}
		if def, ok := opt["default"]; ok {
			set(k, def)
		}
	}
	switch v := feat.Value.(type) {
	case map[string]any:
		userKeys := make([]string, 0, len(v))
		for k := range v {
			userKeys = append(userKeys, k)
		}
		sort.Strings(userKeys)
		for _, k := range userKeys {
			set(k, v[k])
		}
	case nil:
		// typeof null === 'object' in TS: only the defaults.
	default:
		if _, ok := feat.Options["version"]; ok {
			set("version", v)
		}
	}
	return keys, values
}

// jsString converts a JSON value to a string as a JS template literal does.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(x)
	}
}

// escapeQuotesForShell is TS escapeQuotesForShell (for single-quoted strings).
func escapeQuotesForShell(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// FeatureInstallWrapperScript is TS getFeatureInstallWrapperScript: the
// devcontainer-features-install.sh that prints the Feature's banner, sources
// the builtin and the Feature's env files and runs its install.sh. options are
// the Feature's FeatureEnvVariables.
func FeatureInstallWrapperScript(feat features.Feature, set *features.Set, info FeatureFileInfo, options []string) string {
	id := "Unknown"
	if src, ok := set.SourceInfo.(*features.OCISource); ok && src.UserFeatureIdWithoutVersion != "" {
		id = src.UserFeatureIdWithoutVersion
	}
	id = escapeQuotesForShell(id)
	name := feat.Name
	if name == "" {
		name = "Unknown"
	}
	name = escapeQuotesForShell(name)
	description := escapeQuotesForShell(feat.Description)
	version := escapeQuotesForShell(feat.Version)
	documentation := escapeQuotesForShell(feat.DocumentationURL)
	indented := make([]string, len(options))
	for i, o := range options {
		indented[i] = "    " + o
	}
	optionsIndented := escapeQuotesForShell(strings.Join(indented, "\n"))

	warningHeader := ""
	if info.Deprecated {
		warningHeader += `(!) WARNING: Using the deprecated Feature "` + escapeQuotesForShell(feat.ID) + "\". This Feature will no longer receive any further updates/support.\n"
	}
	if len(feat.LegacyIds) > 0 && info.CurrentID != "" && feat.ID != info.CurrentID {
		warningHeader += `(!) WARNING: This feature has been renamed. Please update the reference in devcontainer.json to "` + escapeQuotesForShell(info.CurrentID) + `".`
	}
	echoWarning := ""
	if warningHeader != "" {
		echoWarning = "echo '" + warningHeader + "'"
	}
	errorMessage := `ERROR: Feature "` + name + `" (` + id + `) failed to install!`
	troubleshooting := ""
	if documentation != "" {
		troubleshooting = " Look at the documentation at " + documentation + " for help troubleshooting this error."
	}

	return `#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo '` + errorMessage + troubleshooting + `'
}

trap on_exit EXIT

echo ===========================================================================
` + echoWarning + `
echo 'Feature       : ` + name + `'
echo 'Description   : ` + description + `'
echo 'Id            : ` + id + `'
echo 'Version       : ` + version + `'
echo 'Documentation : ` + documentation + `'
echo 'Options       :'
echo '` + optionsIndented + `'
echo ===========================================================================

set -a
. ../` + FeaturesBuiltinEnvFile + `
. ./` + FeatureEnvFile + `
set +a

chmod +x ./install.sh
./install.sh
`
}

// safeID converts a string to a safe environment variable name.
// Matches TS getSafeId().
func safeID(s string) string {
	// Non-word chars (ASCII, matching RE2 \w) become "_"...
	var b strings.Builder
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := b.String()
	// ...then a leading run of digits/underscores collapses to a single "_".
	k := 0
	for k < len(result) && (result[k] == '_' || (result[k] >= '0' && result[k] <= '9')) {
		k++
	}
	if k > 0 {
		result = "_" + result[k:]
	}
	return strings.ToUpper(result)
}
