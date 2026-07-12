package cli

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	dockermount "github.com/moby/moby/api/types/mount"
)

func metadataMounts(cfg *config.DevContainer) []interface{} {
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
			if mountObj.Readonly != nil {
				serialized["readonly"] = *mountObj.Readonly
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
	return docker.ToImageName(fmt.Sprintf("vsc-%s-%x", basename, hash))
}
