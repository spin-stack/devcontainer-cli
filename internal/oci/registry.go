package oci

// Registry is the seam over the OCI registry operations the CLI actually uses.
// It lists EXACTLY the five methods consumers call (manifest/blob fetch, tag
// listing, artifact and collection-metadata push) so a fake can be injected in
// tests without standing up a real registry. *Client satisfies it.
//
// Only the non-Context method variants are listed here: they are what the CLI
// call sites use today. The Context-aware variants remain on *Client for callers
// that need cancellation.
type Registry interface {
	FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error)
	FetchBlob(ref *Ref, digest string) ([]byte, error)
	GetPublishedTags(ref *Ref) ([]string, error)
	PushArtifact(ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error)
	PushCollectionMetadata(ref *Ref, collectionJSONPath string) (*PushResult, error)
}

// Compile-time assertion that *Client implements Registry.
var _ Registry = (*Client)(nil)
