package oci

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// TS getCredential (httpOCIRegistry.ts) takes the first DEVCONTAINERS_OCI_AUTH
// entry whose first `|` field is the registry, and joins its second and third
// fields as `${split[1]}:${split[2]}`: fields after the third are dropped and a
// missing one reads as undefined.
func TestGetCredentialFromOCIAuthEntryFields(t *testing.T) {
	for _, tt := range []struct {
		name, ociAuth, want string
	}{
		{"fields after the third are dropped", "reg.io|user|pa|ss", "user:pa"},
		{"a missing token is undefined", "reg.io|user,reg.io|other|token", "user:undefined"},
		{"a missing user and token are undefined", "reg.io", "undefined:undefined"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.New(log.Options{Writer: &buf, Level: log.LevelTrace})
			env := map[string]string{"DEVCONTAINERS_OCI_AUTH": tt.ociAuth, "DOCKER_CONFIG": t.TempDir()}
			cred := getCredential(env, "reg.io", logger)
			if cred == nil {
				t.Fatal("credential is nil")
			}
			raw, err := base64.StdEncoding.DecodeString(cred.base64Encoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tt.want {
				t.Fatalf("credential = %q, want %q", raw, tt.want)
			}
			if !strings.Contains(buf.String(), "[httpOci] Using match from DEVCONTAINERS_OCI_AUTH for registry 'reg.io'") {
				t.Fatalf("log = %q", buf.String())
			}
		})
	}
}
