package devtools

import (
	"errors"
	"fmt"
	"strings"
)

// ReleaseInput is what the release workflow knows when it resolves a release:
// the event, the dispatch's version input, the pushed tag, and the repository's
// state (the commit to release, today's CalVer date, and the tags already made).
type ReleaseInput struct {
	Event        string // GITHUB_EVENT_NAME; empty outside CI, which answers as a dispatch
	VersionInput string // a dispatch's version, with or without a leading v
	RefName      string // GITHUB_REF_NAME: the pushed tag
	PushedSHA    string // GITHUB_SHA of a tag push
	HeadSHA      string // HEAD, the commit a dispatch releases
	Today        string // YYYYMMDD in UTC
	TagsToday    int    // tags named v<Today>.*
	TagExists    func(tag string) bool
}

// Release is the version, tag and commit to release.
type Release struct {
	Version, Tag, SHA string
}

// ErrInvalidVersion is a version holding anything but [0-9A-Za-z.+-]: the value
// later reaches git and docker command lines.
var ErrInvalidVersion = errors.New("invalid version")

// ErrTagExists is a version whose tag was already released; releases are immutable.
var ErrTagExists = errors.New("tag already exists")

// ResolveRelease decides the release: a tag push releases that tag; a dispatch
// (or a run outside CI) releases its version input, or the next CalVer of the
// day (YYYYMMDD.NN), at HEAD.
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
