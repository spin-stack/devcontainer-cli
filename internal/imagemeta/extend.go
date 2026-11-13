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

const FeaturesContainerTempDestFolder = "/tmp/dev-container-features"

const FeaturesBuiltinEnvFile = "devcontainer-features.builtin.env"

const (
	FeatureEnvFile            = "devcontainer-features.env"
	FeatureInstallWrapperFile = "devcontainer-features-install.sh"
)

type ExtendImageBuildInfo struct {
	DockerfilePrefixContent string
	DockerfileContent       string
	OverrideTarget          string
	BuildArgs               map[string]string
	BuildKitContexts        map[string]string
	DstFolder               string
}

type FeaturesBuildParams struct {
	BaseName                  string
	FeatureSets               []*features.Set
	Metadata                  []Entry
	ContainerUser, RemoteUser string
	ImageUser                 string
	UseBuildKitContexts       bool
	Syntax                    string
	ConfigContainerEnv        map[string]string
	ConfigEnvOrder            []string // ConfigContainerEnv's member names as declared
	SecretIDs                 []string
	CABundle                  bool
}

const (
	CABundleSecretID = "devcontainer-ca-bundle"
	CABundlePath     = "/run/devcontainer/ca-bundle.crt"
)

func CABundleEnv() []string {
	names := []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}
	env := make([]string, len(names))
	for i, n := range names {
		env[i] = n + "=" + CABundlePath
	}
	return env
}

const ComposeFeatureContentImage = "dev_container_feature_content_temp"

// The global ARG goes in DockerfilePrefixContent because a FROM can only use
// an ARG declared before the first FROM of the combined Dockerfile.
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
		"#{containerEnv}", "",
		"#{devcontainerMetadata}", devcontainerMetadataLabel(p.Metadata),
		"#{containerEnvMetadata}", generateContainerEnvs(p.ConfigContainerEnv, p.ConfigEnvOrder, true),
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

func featureLayers(p FeaturesBuildParams, contentSourceRootPath string) string {
	builtinsEnvFile := path.Join(FeaturesContainerTempDestFolder, FeaturesBuiltinEnvFile)
	var b strings.Builder
	fmt.Fprintf(&b, "RUN \\\necho \"_CONTAINER_USER_HOME=$(%s | cut -d: -f6)\" >> %s && \\\necho \"_REMOTE_USER_HOME=$(%s | cut -d: -f6)\" >> %s\n\n",
		getEntPasswdShellCommand(p.ContainerUser), builtinsEnvFile, getEntPasswdShellCommand(p.RemoteUser), builtinsEnvFile)

	secretMounts := ""
	for _, id := range p.SecretIDs {
		secretMounts += " --mount=type=secret,id=" + id
	}
	installCommandEnvPrefix := ""
	if p.CABundle {
		secretMounts += " --mount=type=secret,id=" + CABundleSecretID + ",target=" + CABundlePath
		installCommandEnvPrefix = strings.Join(CABundleEnv(), " ") + " "
	}
	for _, set := range p.FeatureSets {
		for _, feat := range set.Features {
			b.WriteString(generateContainerEnvs(feat.ContainerEnv, feat.ContainerEnvOrder, false))
			source := path.Join(contentSourceRootPath, feat.ConsecutiveId)
			dest := path.Join(FeaturesContainerTempDestFolder, feat.ConsecutiveId)
			if !p.UseBuildKitContexts {
				runMounts := ""
				if secretMounts != "" {
					runMounts = strings.TrimPrefix(secretMounts, " ") + " "
				}
				fmt.Fprintf(&b, "\nCOPY --chown=root:root --from=dev_containers_feature_content_source %s %s\nRUN %schmod -R 0755 %s \\\n&& cd %s \\\n&& chmod +x ./%s \\\n&& %s./%s\n\n",
					source, dest, runMounts, dest, dest, FeatureInstallWrapperFile, installCommandEnvPrefix, FeatureInstallWrapperFile)
				continue
			}
			fmt.Fprintf(&b, "\nRUN --mount=type=bind,from=dev_containers_feature_content_source,source=%s,target=/tmp/build-features-src/%s%s \\\n    cp -ar /tmp/build-features-src/%s %s \\\n && chmod -R 0755 %s \\\n && cd %s \\\n && chmod +x ./%s \\\n && %s./%s \\\n && rm -rf %s\n\n",
				source, feat.ConsecutiveId, secretMounts, feat.ConsecutiveId, FeaturesContainerTempDestFolder, dest, dest, FeatureInstallWrapperFile, installCommandEnvPrefix, FeatureInstallWrapperFile, dest)
		}
	}
	return b.String()
}

// The lines follow containerEnv's property order (order holds its member
// names as declared), as TS's Object.keys does: without escapeDollar, a value
// that references a variable an earlier line sets expands to that line's
// value.
func generateContainerEnvs(env map[string]string, order []string, escapeDollar bool) string {
	if len(env) == 0 {
		return ""
	}
	special := `"\`
	if escapeDollar {
		special += "$"
	}
	keys := features.PropertyOrder(env, order)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, "ENV "+k+`="`+backslashEscape(env[k], special)+`"`)
	}
	return strings.Join(lines, "\n")
}

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

func devcontainerMetadataLabel(entries []Entry) string {
	entries = nonEmptyEntries(entries)
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		var data strings.Builder
		enc := json.NewEncoder(&data)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(e)
		parts = append(parts, " \\\n"+backslashEscape(strings.TrimSuffix(data.String(), "\n"), `"\$`))
	}
	return "LABEL " + MetadataLabel + `="[` + strings.Join(parts, ",") + ` \` + "\n]\""
}

var regExpSpecial = regexp.MustCompile(`[.*+?^${}()|[\]\\]`)

// The leading space keeps `$(` from forming an arithmetic expansion with the `(`.
func getEntPasswdShellCommand(userNameOrID string) string {
	escapedForShell := regexp.MustCompile(`['\\]`).ReplaceAllString(userNameOrID, `\$0`)
	escapedForRegExp := strings.ReplaceAll(regExpSpecial.ReplaceAllString(userNameOrID, `\$0`), "'", `\'`)
	return fmt.Sprintf(` (command -v getent >/dev/null 2>&1 && getent passwd '%s' || grep -E '^%s|^[^:]*:[^:]*:%s:' /etc/passwd || true)`, escapedForShell, escapedForRegExp, escapedForRegExp)
}

func FeaturesBuiltinEnvContent(containerUser, remoteUser string) string {
	return "_CONTAINER_USER=" + containerUser + "\n_REMOTE_USER=" + remoteUser + "\n"
}

type FeatureFileInfo struct {
	OptionOrder []string
	Deprecated  bool
	CurrentID   string
}

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

func FeatureEnvVariables(feat features.Feature, info FeatureFileInfo) []string {
	keys, values := featureValueObject(feat, info)
	vars := make([]string, 0, len(keys))
	for _, k := range keys {
		vars = append(vars, safeID(k)+`="`+jsString(values[k])+`"`)
	}
	return vars
}

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
		for _, k := range features.PropertyOrder(v, feat.ValueOrder) {
			set(k, v[k])
		}
	case nil:
		// typeof null === 'object' in TS: only the defaults.
	default:
		if _, ok := feat.Options["version"]; ok {
			set("version", v)
		}
	}
	// getFeatureEnvVariables lists the object's Object.keys.
	return features.PropertyOrder(values, keys), values
}

func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64) // mutate-exempt: every negative precision means the shortest representation, so -1 and -2 format alike
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

func escapeQuotesForShell(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

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

func safeID(s string) string {
	// Non-word chars (ASCII, matching RE2 \w) become "_"; the JS regex works
	// on UTF-16 code units, so a rune outside the BMP, a surrogate pair, is two.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	result := b.String()
	k := 0
	for k < len(result) && (result[k] == '_' || (result[k] >= '0' && result[k] <= '9')) {
		k++
	}
	if k > 0 {
		result = "_" + result[k:]
	}
	return strings.ToUpper(result)
}
