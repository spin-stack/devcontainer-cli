package oci

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        Descriptor        `json:"config"`
	Layers        []Layer           `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type Descriptor struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

type Layer struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ManifestContainer struct {
	Manifest      *Manifest
	ManifestBytes []byte
	ContentDigest string
	CanonicalID   string
}
