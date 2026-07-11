package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/pfs"
)

// UpdateRemoteUserUID builds and runs a temporary image that remaps
// the container user's UID/GID to match the host user's UID/GID.
// This prevents permission issues with bind mounts.
// Matches the TS updateRemoteUserUID behavior using scripts/updateUID.Dockerfile.
func UpdateRemoteUserUID(client *Client, logger log.Log, imageName, remoteUser string, hostUID, hostGID int, useBuildx bool) (string, error) {
	if remoteUser == "" || remoteUser == "root" {
		return imageName, nil
	}
	if hostUID == 0 {
		return imageName, nil // host is root, no remap needed
	}

	logger.Write(fmt.Sprintf("Updating remote user UID/GID to %d:%d for user %q", hostUID, hostGID, remoteUser), log.LevelInfo)

	// Find the updateUID.Dockerfile
	// It's shipped alongside the binary in scripts/ or embedded
	dockerfilePath := findUpdateUIDDockerfile()
	if dockerfilePath == "" {
		logger.Write("updateUID.Dockerfile not found, skipping UID remap", log.LevelWarning)
		return imageName, nil
	}

	updatedImageName := imageName + "-uid"

	result, err := client.Build(BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: filepath.Dir(dockerfilePath),
		Tags:        []string{updatedImageName},
		BuildArgs: map[string]string{
			"BASE_IMAGE":  imageName,
			"REMOTE_USER": remoteUser,
			"NEW_UID":     strconv.Itoa(hostUID),
			"NEW_GID":     strconv.Itoa(hostGID),
			"IMAGE_USER":  remoteUser,
		},
		UseBuildx: useBuildx,
	})
	if err != nil {
		return imageName, fmt.Errorf("updateUID build: %w", err)
	}
	if result.ExitCode != 0 {
		logger.Write(fmt.Sprintf("updateUID build failed (exit %d), continuing with original image", result.ExitCode), log.LevelWarning)
		return imageName, nil
	}

	logger.Write(fmt.Sprintf("UID/GID updated: %s → %s", imageName, updatedImageName), log.LevelInfo)
	return updatedImageName, nil
}

// findUpdateUIDDockerfile looks for the updateUID.Dockerfile in common locations.
func findUpdateUIDDockerfile() string {
	candidates := []string{
		// Relative to binary
		"scripts/updateUID.Dockerfile",
		// Relative to working directory
		filepath.Join(".", "scripts", "updateUID.Dockerfile"),
	}

	// Also try relative to the executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "scripts", "updateUID.Dockerfile"),
			filepath.Join(exeDir, "..", "scripts", "updateUID.Dockerfile"),
		)
	}

	for _, c := range candidates {
		if pfs.IsFile(c) {
			return c
		}
	}
	return ""
}
