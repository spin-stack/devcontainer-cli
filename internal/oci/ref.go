package oci

import (
	"fmt"
	"regexp"
	"strings"
)

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

type Ref struct {
	Registry  string
	Owner     string
	Namespace string
	Path      string
	Resource  string
	ID        string
	Version   string
	Tag       string
	Digest    string
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

type CollectionRef struct {
	Registry string
	Path     string
	Resource string
	Tag      string
	Version  string
}

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
		resource = input[:indexOfLastAt]
		if sep := strings.LastIndex(resource, ":"); sep > strings.LastIndex(resource, "/") {
			tag = resource[sep+1:]
			resource = resource[:sep]
		}
		digestWithAlgo := input[indexOfLastAt+1:]
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
		resource = input[:indexOfLastColon]
		tag = input[indexOfLastColon+1:]
	} else {
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

	if parts[1] == "devcontainers-contrib" {
		parts[1] = "devcontainers-extra"
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

func (r *CollectionRef) Ref() *Ref {
	return &Ref{Registry: r.Registry, Path: r.Path, Resource: r.Resource, Tag: r.Tag, Version: r.Version}
}

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
