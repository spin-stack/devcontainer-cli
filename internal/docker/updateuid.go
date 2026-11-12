package docker

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

//go:embed updateUID.Dockerfile
var updateUIDDockerfile string

type UIDUpdate struct {
	BaseImage  string
	FixedImage string
	RemoteUser string
	ImageUser  string
	Platform   string
	UID, GID   int
	ContextDir string
}

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
