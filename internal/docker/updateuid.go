package docker

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// updateUIDDockerfile is the Dockerfile that remaps the container user's UID/GID,
// embedded so the CLI stays a single self-contained binary (no runtime file
// dependency next to the executable).
//
//go:embed updateUID.Dockerfile
var updateUIDDockerfile string

// UIDUpdate describes the image TS updateRemoteUserUID builds, for a single
// container or a Compose service (containerFeatures.ts getRemoteUserUIDUpdateDetails).
type UIDUpdate struct {
	BaseImage  string // image to remap
	FixedImage string // tag of the remapped image
	RemoteUser string
	ImageUser  string
	Platform   string // "os/arch[/variant]" of the base image, "" for none
	UID, GID   int
	// ContextDir is the (empty) build context directory.
	ContextDir string
}

// BuildUIDImage builds u.FixedImage from u.BaseImage with the remote
// user's UID/GID remapped, with the same `docker build` arguments as the TS
// updateRemoteUserUID: IMAGE_USER is the base image's user (the final USER of
// the remapped image), not the remote user.
func BuildUIDImage(ctx context.Context, client *Client, u UIDUpdate) error {
	dir, err := os.MkdirTemp("", "devcontainer-updateuid-")
	if err != nil {
		return fmt.Errorf("updateUID temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	dockerfile := filepath.Join(dir, "updateUID.Dockerfile")
	if err := os.WriteFile(dockerfile, []byte(updateUIDDockerfile), 0o644); err != nil {
		return fmt.Errorf("write updateUID Dockerfile: %w", err)
	}
	contextDir := u.ContextDir
	if contextDir == "" {
		contextDir = dir
	}
	var extra []string
	if u.Platform != "" {
		extra = []string{"--platform", u.Platform}
	}
	res, err := client.Build(ctx, BuildOptions{
		Dockerfile:  dockerfile,
		ContextPath: contextDir,
		Tags:        []string{u.FixedImage},
		ExtraArgs:   extra,
		BuildArgs: map[string]string{
			"BASE_IMAGE":  u.BaseImage,
			"REMOTE_USER": u.RemoteUser,
			"NEW_UID":     strconv.Itoa(u.UID),
			"NEW_GID":     strconv.Itoa(u.GID),
			"IMAGE_USER":  u.ImageUser,
		},
	})
	if err != nil {
		return fmt.Errorf("updateUID build: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("updateUID build failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}
