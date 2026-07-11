package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/product"
	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/tailscale/hujson"
)

var mountSpecPattern = regexp.MustCompile(`^type=(bind|volume),source=([^,]+),target=([^,]+)(?:,external=(true|false))?$`)

// osEnvMap returns the current process environment as a map.
func osEnvMap() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			env[e[:i]] = e[i+1:]
		}
	}
	return env
}

// envSliceToMap converts Docker's []string{"KEY=VALUE"} to map[string]string.
func envSliceToMap(envs []string) map[string]string {
	m := make(map[string]string)
	for _, e := range envs {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

// mergeAdditionalFeatures parses a JSON(C) string of additional features and
// merges them into cfg.Features. Config features have priority — additional
// features are only added if their key doesn't already exist.
// The returned set holds the keys that originated only from --additional-features
// (not present in config.features). 0.88 (#11616) excludes these from the lockfile.
func mergeAdditionalFeatures(cfg *config.DevContainerConfig, additionalFeaturesJSON string) (map[string]bool, error) {
	if additionalFeaturesJSON == "" {
		return nil, nil
	}

	// Parse JSONC (tolerant of comments/trailing commas)
	standardJSON, err := hujson.Standardize([]byte(additionalFeaturesJSON))
	if err != nil {
		return nil, fmt.Errorf("parse additional-features: %w", err)
	}

	var additional map[string]interface{}
	if err := json.Unmarshal(standardJSON, &additional); err != nil {
		return nil, fmt.Errorf("parse additional-features: %w", err)
	}

	if cfg.Features == nil {
		cfg.Features = make(map[string]interface{})
	}

	additionalOnly := make(map[string]bool)
	for k, v := range additional {
		if _, exists := cfg.Features[k]; !exists {
			cfg.Features[k] = v
			additionalOnly[k] = true
		}
	}
	return additionalOnly, nil
}

// validateIDLabels checks that all --id-label values match <name>=<value> format.
func validateIDLabels(labels []string) error {
	for _, l := range labels {
		if !strings.Contains(l, "=") || strings.HasPrefix(l, "=") || strings.HasSuffix(l, "=") {
			return fmt.Errorf("Unmatched argument format: id-label must match <name>=<value>")
		}
	}
	return nil
}

// validateRemoteEnvs checks that all --remote-env values match <name>=<value> format.
func validateRemoteEnvs(envs []string) error {
	for _, e := range envs {
		if !strings.Contains(e, "=") || strings.HasPrefix(e, "=") {
			return fmt.Errorf("Unmatched argument format: remote-env must match <name>=<value>")
		}
	}
	return nil
}

// validateMounts checks that all --mount values match the same format enforced by TS.
func validateMounts(mounts []string) error {
	for _, m := range mounts {
		if !mountSpecPattern.MatchString(m) {
			return fmt.Errorf("Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]")
		}
	}
	return nil
}

// logDimensions returns Dimensions for the logger if terminal size was provided.
// cliVersion returns the CLI version for the log banner (set via ldflags).
func cliVersion() string {
	return product.GetConfig().Version
}

// appPortPublishArgs turns the config `appPort` (number | string | array) into
// `docker create` publish flags, matching the TS CLI: a number N publishes
// 127.0.0.1:N:N, a string is used verbatim (e.g. "8080:80").
func appPortPublishArgs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var items []interface{}
	switch t := v.(type) {
	case []interface{}:
		items = t
	default:
		items = []interface{}{t}
	}
	var args []string
	for _, item := range items {
		switch p := item.(type) {
		case float64:
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", int(p), int(p)))
		case string:
			if p != "" {
				args = append(args, "-p", p)
			}
		}
	}
	return args
}

func logDimensions(columns, rows int) *log.Dimensions {
	if columns > 0 || rows > 0 {
		return &log.Dimensions{Columns: columns, Rows: rows}
	}
	return nil
}

// validateTerminalImplications checks bidirectional implications between
// terminal-columns and terminal-rows (matches yargs .implies()).
func validateTerminalImplications(columns, rows int) error {
	if columns > 0 && rows == 0 {
		return fmt.Errorf("Implications failed:\n terminal-columns -> terminal-rows")
	}
	if rows > 0 && columns == 0 {
		return fmt.Errorf("Implications failed:\n terminal-rows -> terminal-columns")
	}
	return nil
}

// validateEnum checks that a flag value is one of the allowed choices.
func validateEnum(flagName, value string, choices []string) error {
	for _, c := range choices {
		if value == c {
			return nil
		}
	}
	return fmt.Errorf("Invalid value %q for --%s. Choose from: %s", value, flagName, strings.Join(choices, ", "))
}

// checkGPUAvailability determines if GPU should be enabled based on the flag value.
func checkGPUAvailability(gpuFlag string, dockerClient *docker.Client) bool {
	switch gpuFlag {
	case "all":
		return true
	case "none":
		return false
	default: // "detect"
		// Match the TS CLI: the nvidia runtime is present only when docker info
		// reports "nvidia-container-runtime". When absent, the Go template renders
		// "<no value>", which is non-empty — so an emptiness check wrongly enabled
		// GPUs and made `up` fail on hosts without nvidia.
		res, err := dockerClient.Run("info", "-f", "{{.Runtimes.nvidia}}")
		return err == nil && strings.Contains(string(res.Stdout), "nvidia-container-runtime")
	}
}

// gpuRequested interprets hostRequirements.gpu (bool | "optional" | object) with
// JS truthiness, matching the TS CLI: false/null/absent → no GPU; true, "optional"
// or an object → GPU requested.
func gpuRequested(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "false"
}

// readSecretsFile reads a JSON file {"KEY": "VALUE", ...} and returns ["KEY=VALUE", ...].
func readSecretsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	var envs []string
	for k, v := range secrets {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	return envs, nil
}

// secretValuesFromFile returns just the secret values from a secrets file, for
// log masking. Best-effort: returns nil on any error (the eventual load of the
// same file surfaces real problems).
func secretValuesFromFile(path string) []string {
	if path == "" {
		return nil
	}
	envs, err := readSecretsFile(path)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(envs))
	for _, e := range envs {
		if i := strings.IndexByte(e, '='); i >= 0 {
			values = append(values, e[i+1:])
		}
	}
	return values
}

func metadataMounts(cfg *config.DevContainerConfig) []interface{} {
	if len(cfg.Mounts) == 0 {
		return nil
	}

	mounts := make([]interface{}, 0, len(cfg.Mounts))
	for _, mountValue := range cfg.Mounts {
		if spec, ok := mountValue.AsString(); ok && spec != "" {
			mounts = append(mounts, spec)
			continue
		}
		if mountObj, ok := mountValue.AsMount(); ok {
			serialized := map[string]interface{}{}
			if mountObj.Type != "" {
				serialized["type"] = mountObj.Type
			}
			if mountObj.Source != "" {
				serialized["source"] = mountObj.Source
			}
			if mountObj.Target != "" {
				serialized["target"] = mountObj.Target
			}
			if mountObj.External != nil {
				serialized["external"] = *mountObj.External
			}
			mounts = append(mounts, serialized)
		}
	}

	if len(mounts) == 0 {
		return nil
	}
	return mounts
}

func mountsFromMetadata(entries []interface{}, devcontainerID string) ([]dockermount.Mount, error) {
	mounts := make([]dockermount.Mount, 0, len(entries))
	for _, entry := range entries {
		mountSpec, err := mountFromMetadata(entry, devcontainerID)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mountSpec)
	}
	return mounts, nil
}

// composeServiceImage returns the `image:` of a service from parsed
// `docker compose config` output, or "" if not set (e.g. a build-only service).
func composeServiceImage(composeConfig map[string]interface{}, service string) string {
	services, ok := composeConfig["services"].(map[string]interface{})
	if !ok {
		return ""
	}
	svc, ok := services[service].(map[string]interface{})
	if !ok {
		return ""
	}
	image, _ := svc["image"].(string)
	return image
}

func composeVolumeSpecsFromMetadata(entries []interface{}, devcontainerID string) ([]string, []string, error) {
	specs := make([]string, 0, len(entries))
	namedVolumes := []string{}
	for _, entry := range entries {
		mountSpec, err := mountFromMetadata(entry, devcontainerID)
		if err != nil {
			return nil, nil, err
		}

		spec := mountSpec.Target
		if mountSpec.Source != "" {
			spec = mountSpec.Source + ":" + mountSpec.Target
		}
		if mountSpec.ReadOnly {
			spec += ":ro"
		}
		specs = append(specs, spec)

		if mountSpec.Type == dockermount.TypeVolume && mountSpec.Source != "" && !strings.Contains(mountSpec.Source, "/") {
			namedVolumes = append(namedVolumes, mountSpec.Source)
		}
	}
	return specs, namedVolumes, nil
}

func mountFromMetadata(entry interface{}, devcontainerID string) (dockermount.Mount, error) {
	switch raw := entry.(type) {
	case string:
		spec := strings.ReplaceAll(raw, "${devcontainerId}", devcontainerID)
		return docker.ParseMountSpec(spec)
	case map[string]interface{}:
		target, _ := raw["target"].(string)
		if target == "" {
			return dockermount.Mount{}, fmt.Errorf("mount requires a target/destination")
		}

		source, _ := raw["source"].(string)
		source = strings.ReplaceAll(source, "${devcontainerId}", devcontainerID)
		mountType, _ := raw["type"].(string)
		if mountType == "" {
			mountType = string(dockermount.TypeBind)
		}

		result := dockermount.Mount{
			Type:   dockermount.Type(mountType),
			Source: source,
			Target: target,
		}
		if readonly, ok := raw["readonly"].(bool); ok {
			result.ReadOnly = readonly
		}
		return result, nil
	default:
		return dockermount.Mount{}, fmt.Errorf("unsupported mount metadata type %T", entry)
	}
}

// folderImageName generates a deterministic image name from a workspace folder path.
// Matches the TS CLI's getFolderImageName: "vsc-{basename}-{sha256hex}"
func folderImageName(folderPath string) string {
	basename := filepath.Base(folderPath)
	hash := sha256.Sum256([]byte(folderPath))
	return docker.ToDockerImageName(fmt.Sprintf("vsc-%s-%x", basename, hash))
}
