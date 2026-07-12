package oci

import "context"

// Registry is the seam over the OCI registry operations the CLI actually uses.
// It lists EXACTLY the five methods consumers call (manifest/blob fetch, tag
// listing, artifact and collection-metadata push) so a fake can be injected in
// tests without standing up a real registry. *Client satisfies it.
//
// The fetch/list methods keep their non-Context convenience variants (they are
// short reads, and each now applies a default per-operation deadline); a Context
// variant is exposed where a caller already holds the command context (e.g.
// publish) so the operation participates in the CLI's signal-cancellation.
type Registry interface {
	FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error)
	FetchBlob(ref *Ref, digest string) ([]byte, error)
	GetPublishedTags(ref *Ref) ([]string, error)
	GetPublishedTagsContext(ctx context.Context, ref *Ref) ([]string, error)
	PushArtifact(ctx context.Context, ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error)
	PushCollectionMetadata(ctx context.Context, ref *Ref, collectionJSONPath string) (*PushResult, error)
}

// Compile-time assertion that *Client implements Registry.
var _ Registry = (*Client)(nil)
