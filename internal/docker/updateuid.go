package docker

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/devcontainers/cli/internal/log"
)

// updateUIDDockerfile is the Dockerfile that remaps the container user's UID/GID,
// embedded so the CLI stays a single self-contained binary (no runtime file
// dependency next to the executable).
//
//go:embed updateUID.Dockerfile
var updateUIDDockerfile string

// UpdateRemoteUserUID builds and runs a temporary image that remaps the container
// user's UID/GID to match the host user's, preventing bind-mount permission
// issues. Matches the TS updateRemoteUserUID behavior.
func UpdateRemoteUserUID(ctx context.Context, client *Client, logger log.Logger, imageName, remoteUser string, hostUID, hostGID int, useBuildx bool) (string, error) {
	if remoteUser == "" || remoteUser == "root" {
		return imageName, nil
	}
	if hostUID == 0 {
		return imageName, nil // host is root, no remap needed
	}

	logger.Write(fmt.Sprintf("Updating remote user UID/GID to %d:%d for user %q", hostUID, hostGID, remoteUser), log.LevelInfo)

	// Materialize the embedded Dockerfile into a temp build context.
	tmpDir, err := os.MkdirTemp("", "devcontainer-updateuid-")
	if err != nil {
		return imageName, fmt.Errorf("updateUID temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(updateUIDDockerfile), 0o644); err != nil {
		return imageName, fmt.Errorf("write updateUID Dockerfile: %w", err)
	}

	updatedImageName := imageName + "-uid"

	result, err := client.Build(ctx, BuildOptions{
		Dockerfile:  dockerfilePath,
		ContextPath: tmpDir,
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
