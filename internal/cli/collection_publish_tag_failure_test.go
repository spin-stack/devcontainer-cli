package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

type pushRegistry struct {
	host    string
	failTag string

	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	attempts  map[string][]string
}

func newPushRegistry(t *testing.T, failTag string) *pushRegistry {
	t.Helper()
	r := &pushRegistry{failTag: failTag, blobs: map[string][]byte{}, manifests: map[string][]byte{}, attempts: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	// A localhost registry is plain HTTP for both CLIs.
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r.host = "localhost:" + port
	return r
}

func (r *pushRegistry) tagAttempts(repo string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.attempts[repo])
}

func (r *pushRegistry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := strings.TrimPrefix(req.URL.Path, "/v2/")
	notFound := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"not found"}]}`))
	}
	switch {
	case path == "" || path == "/":
		w.WriteHeader(http.StatusOK)
	case strings.HasSuffix(path, "/tags/list"):
		notFound()
	case strings.Contains(path, "/blobs/uploads/") && req.Method == http.MethodPost:
		w.Header().Set("Location", req.URL.Path+"session")
		w.WriteHeader(http.StatusAccepted)
	case strings.Contains(path, "/blobs/uploads/") && req.Method == http.MethodPut:
		body, _ := io.ReadAll(req.Body)
		digest := req.URL.Query().Get("digest")
		r.blobs[digest] = body
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case strings.Contains(path, "/blobs/"):
		body, ok := r.blobs[path[strings.LastIndex(path, "/")+1:]]
		if !ok {
			notFound()
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if req.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	case strings.Contains(path, "/manifests/"):
		i := strings.Index(path, "/manifests/")
		repo, ref := path[:i], path[i+len("/manifests/"):]
		if req.Method != http.MethodPut {
			body, ok := r.manifests[repo+":"+ref]
			if !ok {
				notFound()
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			if req.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
			return
		}
		r.attempts[repo] = append(r.attempts[repo], ref)
		if ref == r.failTag {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"code":"TAG_INVALID","message":"tag rejected by test"}]}`))
			return
		}
		body, _ := io.ReadAll(req.Body)
		r.manifests[repo+":"+ref] = body
		sum := sha256.Sum256(body)
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
		w.WriteHeader(http.StatusCreated)
	default:
		notFound()
	}
}

func writeTagFailureCollection(t *testing.T, collectionType string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if collectionType == "feature" {
		writeSeamFeature(t, src, "good", "1.0.0")
		return src
	}
	writeFile(t, filepath.Join(src, "good", "devcontainer-template.json"), `{"id":"good","version":"1.0.0","name":"Good"}`, 0o644)
	writeFile(t, filepath.Join(src, "good", ".devcontainer.json"), `{"image":"alpine"}`, 0o644)
	return src
}

func TestPublishCollection_FailedTagPushExits(t *testing.T) {
	for _, collectionType := range []string{"feature", "template"} {
		t.Run(collectionType, func(t *testing.T) {
			reg := newPushRegistry(t, "1.0")
			out := &captureOutput{}
			logger := log.New(log.Options{Level: log.LevelInfo, Format: "text", Writer: out.Stderr()})
			client := oci.NewClientWithAuthPolicy(logger, map[string]string{"DOCKER_CONFIG": t.TempDir()}, oci.DefaultAuthPolicy())

			err := publishCollectionWith(t.Context(), out, client, writeTagFailureCollection(t, collectionType), reg.host, "me/coll", collectionType, "info")
			if exit, ok := errors.AsType[*coreerrors.ExitCodeError](err); !ok || exit.Code != 1 {
				t.Fatalf("err = %v, want exit code 1 after the rejected tag", err)
			}
			if out.out.Len() != 0 {
				t.Errorf("stdout = %q, want no result after a failed tag push", out.out.String())
			}

			resource := reg.host + "/me/coll/good"
			stderr := out.err.String()
			for _, want := range []string{
				"Failed to PUT manifest for tag 1.0\n",
				fmt.Sprintf("(!) ERR: Failed to publish %s: '%s'", collectionType, resource),
				fmt.Sprintf("(!) ERR: Failed to publish '%s'", resource),
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr is missing %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, "Published "+collectionType) {
				t.Errorf("stderr reports a successful publish:\n%s", stderr)
			}

			if got, want := reg.tagAttempts("me/coll/good"), []string{"1", "1.0"}; !slices.Equal(got, want) {
				t.Errorf("tag PUTs = %v, want %v: no tag is tried after the first failure", got, want)
			}
			if got := reg.tagAttempts("me/coll"); len(got) != 0 {
				t.Errorf("collection metadata PUTs = %v, want none after a failed publish", got)
			}
		})
	}
}
