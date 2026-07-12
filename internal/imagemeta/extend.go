package imagemeta

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/features"
)

// ExtendImageBuildInfo holds the Dockerfile and build context needed
// to extend a base image with features.
type ExtendImageBuildInfo struct {
	DockerfilePrefixContent string
	DockerfileContent       string
	OverrideTarget          string
	BuildArgs               map[string]string
	BuildKitContexts        map[string]string
	DstFolder               string
}

// GenerateExtendImageBuild creates the Dockerfile content for installing
// features on top of a base image. Matches the TS getFeaturesBuildOptions().
func GenerateExtendImageBuild(
	baseImage string,
	featureSets []*features.Set,
	metadata []Entry,
	containerUser, remoteUser string,
	useBuildKitContexts bool,
	configContainerEnv map[string]string,
) *ExtendImageBuildInfo {
	if len(featureSets) == 0 {
		// No features — just add metadata label
		df := docker.NewDockerfileBuilder()
		df.Arg("_DEV_CONTAINERS_BASE_IMAGE", "placeholder")
		prefix := df.String()

		df2 := docker.NewDockerfileBuilder()
		df2.BlankLine()
		df2.From("$_DEV_CONTAINERS_BASE_IMAGE").As("dev_containers_target_stage")
		df2.Label(MetadataLabel, GenerateMetadataLabel(metadata))

		return &ExtendImageBuildInfo{
			DockerfilePrefixContent: prefix,
			DockerfileContent:       df2.String(),
			OverrideTarget:          "dev_containers_target_stage",
			BuildArgs:               map[string]string{"_DEV_CONTAINERS_BASE_IMAGE": baseImage},
			BuildKitContexts:        map[string]string{},
		}
	}

	df := docker.NewDockerfileBuilder()

	// Preamble
	df.Arg("_DEV_CONTAINERS_BASE_IMAGE", "placeholder")

	if useBuildKitContexts {
		df.From("scratch").As("dev_containers_feature_content_source")
	}

	// Main stage
	df.BlankLine()
	df.From("$_DEV_CONTAINERS_BASE_IMAGE").As("dev_containers_target_stage")
	df.User("root")

	// Install each feature
	for i, fs := range featureSets {
		if len(fs.Features) == 0 {
			continue
		}
		feat := fs.Features[0]
		featureDir := fmt.Sprintf("_dev_container_feature_%d", i)
		dstPath := fmt.Sprintf("/tmp/build-features/%s", featureDir)

		// Emit the feature's containerEnv as ENV *before* the install RUN, matching
		// the TS CLI. Docker expands ${VAR} references (e.g. PATH="...:${PATH}") at
		// ENV time, so install.sh runs with a correct PATH. These ENVs also persist
		// into the final image (the runtime containerEnv).
		for _, env := range generatePersistentContainerEnvVars(feat) {
			df.EnvRaw(env)
		}

		if useBuildKitContexts {
			df.Copy(featureDir+"/", dstPath+"/").From("dev_containers_feature_content_source")
		} else {
			df.Copy(featureDir+"/", dstPath+"/")
		}

		// Feature option env vars must apply to install.sh, not to the preceding
		// chmod. In POSIX sh, `KEY=v chmod ... && install.sh` scopes KEY to chmod
		// only, so options and _REMOTE_USER/_CONTAINER_USER never reach install.sh
		// (features install with empty options — silently wrong images or broken
		// builds). Prefix the assignments directly onto install.sh instead.
		installCmd := fmt.Sprintf("%s/install.sh", dstPath)
		if envs := generateFeatureBuildEnvVars(feat, containerUser, remoteUser); len(envs) > 0 {
			installCmd = strings.Join(envs, " ") + " " + installCmd
		}
		runCmd := fmt.Sprintf("chmod +x %s/install.sh && %s", dstPath, installCmd)
		df.Run(runCmd)
	}

	// Config-level containerEnv (overrides feature env vars)
	if len(configContainerEnv) > 0 {
		keys := make([]string, 0, len(configContainerEnv))
		for k := range configContainerEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			df.Env(k, configContainerEnv[k]) // escaping handled by builder
		}
	}

	// Metadata label — escaping handled by builder
	df.BlankLine()
	df.Label(MetadataLabel, GenerateMetadataLabel(metadata))

	if containerUser != "root" {
		df.User(containerUser)
	}

	buildArgs := map[string]string{"_DEV_CONTAINERS_BASE_IMAGE": baseImage}
	buildKitContexts := map[string]string{}
	prefix := ""
	if useBuildKitContexts {
		buildKitContexts["dev_containers_feature_content_source"] = "."
		// The feature build passes --build-context; that requires a docker/dockerfile
		// frontend >= 1.4. Declare it as the first line (matching the TS CLI), or the
		// build fails on a Docker whose default frontend predates build contexts.
		prefix = "# syntax=docker/dockerfile:1.4\n"
	}

	return &ExtendImageBuildInfo{
		DockerfilePrefixContent: prefix,
		DockerfileContent:       df.String(),
		OverrideTarget:          "dev_containers_target_stage",
		BuildArgs:               buildArgs,
		BuildKitContexts:        buildKitContexts,
	}
}

// GenerateExtendImageBuildForCompose generates feature Dockerfile content for injection
// into an existing compose Dockerfile. Instead of using ARG/FROM $_DEV_CONTAINERS_BASE_IMAGE,
// it directly references the named base stage.
func GenerateExtendImageBuildForCompose(
	baseStageName string,
	featureSets []*features.Set,
	metadata []Entry,
	containerUser, remoteUser string,
	configContainerEnv map[string]string,
) string {
	df := docker.NewDockerfileBuilder()

	df.BlankLine()
	df.From(baseStageName).As("dev_containers_target_stage")
	df.User("root")

	for i, fs := range featureSets {
		if len(fs.Features) == 0 {
			continue
		}
		feat := fs.Features[0]
		featureDir := fmt.Sprintf("_dev_container_feature_%d", i)
		dstPath := fmt.Sprintf("/tmp/build-features/%s", featureDir)

		// Emit the feature's containerEnv as ENV *before* the install RUN (Docker
		// expands ${VAR} references like PATH="...:${PATH}" at ENV time), matching
		// the TS CLI. Also persists into the final image.
		for _, env := range generatePersistentContainerEnvVars(feat) {
			df.EnvRaw(env)
		}

		df.Copy(featureDir+"/", dstPath+"/")

		// Feature option env vars must apply to install.sh, not to the preceding
		// chmod. In POSIX sh, `KEY=v chmod ... && install.sh` scopes KEY to chmod
		// only, so options and _REMOTE_USER/_CONTAINER_USER never reach install.sh
		// (features install with empty options — silently wrong images or broken
		// builds). Prefix the assignments directly onto install.sh instead.
		installCmd := fmt.Sprintf("%s/install.sh", dstPath)
		if envs := generateFeatureBuildEnvVars(feat, containerUser, remoteUser); len(envs) > 0 {
			installCmd = strings.Join(envs, " ") + " " + installCmd
		}
		runCmd := fmt.Sprintf("chmod +x %s/install.sh && %s", dstPath, installCmd)
		df.Run(runCmd)
	}

	if len(configContainerEnv) > 0 {
		keys := make([]string, 0, len(configContainerEnv))
		for k := range configContainerEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			df.Env(k, configContainerEnv[k])
		}
	}

	df.BlankLine()
	df.Label(MetadataLabel, GenerateMetadataLabel(metadata))

	if containerUser != "root" {
		df.User(containerUser)
	}

	return df.String()
}

// getEntPasswdExpr returns a shell expression that prints the /etc/passwd line
// for a username OR uid, preferring getent and falling back to grep on
// getent-less images (alpine/busybox) — matching the TS getEntPasswdShellCommand.
func getEntPasswdExpr(userNameOrID string) string {
	shellEsc := strings.ReplaceAll(userNameOrID, "'", `'\''`)
	reEsc := regexp.QuoteMeta(userNameOrID)
	return fmt.Sprintf(`(command -v getent >/dev/null 2>&1 && getent passwd '%s' || grep -E '^%s|^[^:]*:[^:]*:%s:' /etc/passwd || true)`, shellEsc, reEsc, reEsc)
}

// safeID converts a string to a safe environment variable name.
// Matches TS getSafeId().
func safeID(s string) string {
	re := regexp.MustCompile(`[^\w_]`)
	result := re.ReplaceAllString(s, "_")
	reLeading := regexp.MustCompile(`^[\d_]+`)
	result = reLeading.ReplaceAllString(result, "_")
	return strings.ToUpper(result)
}

// generateFeatureBuildEnvVars produces shell assignments used only while the
// feature install script is running. These should not persist in the final image.
func generateFeatureBuildEnvVars(feat features.Feature, containerUser, remoteUser string) []string {
	var envs []string
	safeID := safeID(feat.ID)

	envs = append(envs,
		fmt.Sprintf("_CONTAINER_USER=%s", shellSingleQuote(containerUser)),
		fmt.Sprintf("_REMOTE_USER=%s", shellSingleQuote(remoteUser)),
		// Resolve the users' home dirs from /etc/passwd at RUN time (a feature like
		// common-utils may have just created the user, so a later feature must see
		// its home) — matching the TS CLI's _REMOTE_USER_HOME / _CONTAINER_USER_HOME
		// builtins. Command substitution, so these must not be single-quoted.
		// Note the space after `$(`: without it the leading `(` makes the shell
		// parse `$((` as arithmetic expansion and fail.
		fmt.Sprintf(`_CONTAINER_USER_HOME="$( %s | cut -d: -f6)"`, getEntPasswdExpr(containerUser)),
		fmt.Sprintf(`_REMOTE_USER_HOME="$( %s | cut -d: -f6)"`, getEntPasswdExpr(remoteUser)),
	)

	// Main value — TS normalizes option objects to true and only emits the
	// feature's primary BUILD_ARG plus per-option env vars.
	if feat.Value != nil {
		switch v := normalizeFeatureValue(feat.Value).(type) {
		case bool:
			envs = append(envs, fmt.Sprintf("_BUILD_ARG_%s=%s", safeID, shellSingleQuote(fmt.Sprintf("%t", v))))
		case string:
			if v != "" {
				envs = append(envs, fmt.Sprintf("_BUILD_ARG_%s=%s", safeID, shellSingleQuote(v)))
			}
		default:
			data, _ := json.Marshal(v)
			str := string(data)
			if str != "{}" && str != "null" {
				envs = append(envs, fmt.Sprintf("_BUILD_ARG_%s=%s", safeID, shellSingleQuote(str)))
			}
		}
	}

	// Per-option env vars from feature metadata
	// Feature options (from devcontainer-feature.json) are set as env vars
	// so install.sh can read them. E.g., VERSION=latest, GREETING=hello
	if feat.Options != nil {
		optionNames := make([]string, 0, len(feat.Options))
		for optName := range feat.Options {
			optionNames = append(optionNames, optName)
		}
		sort.Strings(optionNames)
		for _, optName := range optionNames {
			optDef := feat.Options[optName]
			optMap, ok := optDef.(map[string]interface{})
			if !ok {
				continue
			}
			// Get user-provided value or default
			defaultVal := optMap["default"]
			val := defaultVal

			// Check if user provided a value in UserOptions
			if feat.UserOptions != nil {
				if userVal, ok := feat.UserOptions[optName]; ok {
					val = userVal
				}
			}

			if val != nil {
				upperName := strings.ToUpper(optName)
				strVal := fmt.Sprintf("%v", val)
				envs = append(envs, fmt.Sprintf("%s=%s", upperName, shellSingleQuote(strVal)))
				envs = append(envs, fmt.Sprintf("_BUILD_ARG_%s_%s=%s", safeID, strings.ToUpper(optName), shellSingleQuote(strVal)))
			}
		}
	}

	// NOTE: the feature's containerEnv is intentionally NOT added here. It is
	// emitted as `ENV KEY="value"` before the install RUN (see the generation
	// loops), so Docker expands ${VAR} references (e.g. PATH="...:${PATH}").
	// Inlining it single-quoted broke such values (literal ${PATH} → broken PATH
	// → install.sh's `env bash` failed with exit 127).

	return envs
}

func generatePersistentContainerEnvVars(feat features.Feature) []string {
	if len(feat.ContainerEnv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(feat.ContainerEnv))
	for k := range feat.ContainerEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envs := make([]string, 0, len(keys))
	for _, k := range keys {
		envs = append(envs, fmt.Sprintf("%s=%q", k, feat.ContainerEnv[k]))
	}
	return envs
}

func normalizeFeatureValue(v interface{}) interface{} {
	if _, ok := v.(map[string]interface{}); ok {
		return true
	}
	return v
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
