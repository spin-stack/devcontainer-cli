package oci

// Manifest represents an OCI image manifest.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        Descriptor        `json:"config"`
	Layers        []Layer           `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Descriptor is a content-addressable reference.
type Descriptor struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

// Layer represents a content layer in the manifest.
type Layer struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ManifestContainer wraps a manifest with computed metadata.
type ManifestContainer struct {
	Manifest      *Manifest
	ManifestBytes []byte
	ContentDigest string // sha256:...
	CanonicalID   string // resource@sha256:...
}
