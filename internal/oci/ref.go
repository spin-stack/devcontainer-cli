package oci

import (
	"fmt"
	"regexp"
	"strings"
)

// Mediatypes for devcontainer OCI artifacts.
const (
	ManifestMediaType        = "application/vnd.devcontainers"
	TarLayerMediaType        = "application/vnd.devcontainers.layer.v1+tar"
	CollectionLayerMediaType = "application/vnd.devcontainers.collection.layer.v1+json"
	OCIManifestMediaType     = "application/vnd.oci.image.manifest.v1+json"
)

var (
	regexForPath            = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(\/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
	regexForVersionOrDigest = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
)

// Ref represents a parsed OCI reference for a Feature or Template.
// e.g., ghcr.io/devcontainers/features/go:1.0.0
type Ref struct {
	Registry  string // "ghcr.io"
	Owner     string // "devcontainers"
	Namespace string // "devcontainers/features"
	Path      string // "devcontainers/features/go"
	Resource  string // "ghcr.io/devcontainers/features/go"
	ID        string // "go"
	Version   string // tag or digest (most specific)
	Tag       string // "1.0.0" (empty if digest)
	Digest    string // "sha256:..." (empty if tag)
}

// Scheme is the transport the registry is addressed over, mirroring the TS
// getRegistryScheme (containerCollectionsOCI.ts): plain HTTP for a localhost
// registry, HTTPS otherwise. It is part of the `read-configuration` output
// (featuresConfiguration.featureSets[].sourceInformation.featureRef.scheme).
func (r *Ref) Scheme() string {
	host, _, _ := strings.Cut(r.Registry, ":")
	if strings.EqualFold(host, "localhost") {
		return "http"
	}
	return "https"
}

// CollectionRef represents a collection metadata artifact reference.
// e.g., ghcr.io/devcontainers/features:latest
type CollectionRef struct {
	Registry string // "ghcr.io"
	Path     string // "devcontainers/features"
	Resource string // "ghcr.io/devcontainers/features"
	Tag      string // always "latest"
	Version  string // always "latest"
}

// ParseRef parses an OCI reference string into a structured Ref.
// Matches the TS implementation in containerCollectionsOCI.ts:getRef().
func ParseRef(input string) (*Ref, error) {
	input = strings.ToLower(input)

	if strings.HasPrefix(input, ".") {
		return nil, fmt.Errorf("input %q must not start with '.'", input)
	}

	var resource, tag, digest string

	indexOfLastAt := strings.LastIndex(input, "@")
	indexOfLastColon := strings.LastIndex(input, ":")
	indexOfLastSlash := strings.LastIndex(input, "/")

	if indexOfLastAt != -1 {
		// Digest reference: registry/path@sha256:hex
		resource = input[:indexOfLastAt]
		digestWithAlgo := input[indexOfLastAt+1:]
		// TS splits on every colon and wants exactly two parts.
		parts := strings.Split(digestWithAlgo, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid digest format %q, expected 'sha256:hex'", digestWithAlgo)
		}
		if parts[0] != "sha256" {
			return nil, fmt.Errorf("unsupported digest algorithm %q, expected 'sha256'", parts[0])
		}
		if !regexForVersionOrDigest.MatchString(parts[1]) {
			return nil, fmt.Errorf("digest %q does not match expected pattern", parts[1])
		}
		digest = digestWithAlgo
	} else if indexOfLastColon > indexOfLastSlash {
		// Tag reference: registry/path:tag. TS also checks
		// indexOfLastColon !== -1, which this implies: the last slash is never
		// before index -1.
		resource = input[:indexOfLastColon]
		tag = input[indexOfLastColon+1:]
	} else {
		// No tag or digest — default to "latest"
		resource = input
		tag = "latest"
	}

	if tag != "" && !regexForVersionOrDigest.MatchString(tag) {
		return nil, fmt.Errorf("tag %q does not match expected pattern", tag)
	}

	parts := strings.Split(resource, "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("resource %q must have at least registry/namespace/id", resource)
	}

	registry := parts[0]
	owner := parts[1]
	id := parts[len(parts)-1]
	namespace := strings.Join(parts[1:len(parts)-1], "/")
	path := namespace + "/" + id

	if !regexForPath.MatchString(path) {
		return nil, fmt.Errorf("path %q does not match expected pattern", path)
	}

	version := digest
	if version == "" {
		version = tag
	}
	if version == "" {
		version = "latest"
	}

	return &Ref{
		Registry:  registry,
		Owner:     owner,
		Namespace: namespace,
		Path:      path,
		Resource:  resource,
		ID:        id,
		Version:   version,
		Tag:       tag,
		Digest:    digest,
	}, nil
}

// Ref is the collection as the repository the registry client pushes to.
func (r *CollectionRef) Ref() *Ref {
	return &Ref{Registry: r.Registry, Path: r.Path, Resource: r.Resource, Tag: r.Tag, Version: r.Version}
}

// ParseCollectionRef parses registry + namespace into a CollectionRef, as TS
// getCollectionRef does: the namespace, lowercased, is the whole path.
func ParseCollectionRef(registry, namespace string) (*CollectionRef, error) {
	registry = strings.ToLower(registry)
	namespace = strings.ToLower(namespace)
	path := namespace
	resource := registry + "/" + path

	if !regexForPath.MatchString(path) {
		return nil, fmt.Errorf("path %q does not match expected pattern", path)
	}

	return &CollectionRef{
		Registry: registry,
		Path:     path,
		Resource: resource,
		Tag:      "latest",
		Version:  "latest",
	}, nil
}
