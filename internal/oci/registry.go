package oci

import "context"

type Registry interface {
	FetchManifest(ref *Ref, expectedDigest string) (*ManifestContainer, error)
	FetchBlob(ref *Ref, digest string) ([]byte, error)
	GetPublishedTags(ref *Ref) ([]string, error)
	GetPublishedTagsContext(ctx context.Context, ref *Ref) ([]string, error)
	PushArtifact(ctx context.Context, ref *Ref, tgzPath string, tags []string, collectionType string, annotations map[string]string) (*PushResult, error)
	PushCollectionMetadata(ctx context.Context, ref *Ref, collectionJSONPath string) (*PushResult, error)
}

var _ Registry = (*Client)(nil)
