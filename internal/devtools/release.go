package devtools

import (
	"errors"
	"fmt"
	"strings"
)

type ReleaseInput struct {
	Event        string
	VersionInput string
	RefName      string
	PushedSHA    string
	HeadSHA      string
	Today        string
	TagsToday    int
	TagExists    func(tag string) bool
}

type Release struct {
	Version, Tag, SHA string
}

var ErrInvalidVersion = errors.New("invalid version")

var ErrTagExists = errors.New("tag already exists")

func ResolveRelease(in ReleaseInput) (Release, error) {
	if in.Event != "" && in.Event != "workflow_dispatch" {
		return Release{Version: strings.TrimPrefix(in.RefName, "v"), Tag: in.RefName, SHA: in.PushedSHA}, nil
	}
	version := strings.TrimPrefix(in.VersionInput, "v")
	if version == "" {
		version = fmt.Sprintf("%s.%02d", in.Today, in.TagsToday+1)
	}
	if !validVersion(version) {
		return Release{}, fmt.Errorf("%w '%s': use only [0-9A-Za-z.+-]", ErrInvalidVersion, version)
	}
	tag := "v" + version
	if in.TagExists != nil && in.TagExists(tag) {
		return Release{}, fmt.Errorf("%w: %s — releases are immutable, bump the build number", ErrTagExists, tag)
	}
	return Release{Version: version, Tag: tag, SHA: in.HeadSHA}, nil
}

func validVersion(v string) bool {
	return v != "" && strings.Trim(v, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.+-") == ""
}
