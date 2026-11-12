package cli

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"maps"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
	"github.com/moby/moby/api/types/container"
	dockermount "github.com/moby/moby/api/types/mount"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "devcontainer-cli test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca *testCA) serverPEM(t *testing.T, ip net.IP) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: ip.String()},
		IPAddresses:  []net.IP{ip},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func writeTestFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveCABundle(t *testing.T) {
	if got, err := resolveCABundle(""); got != "" || err != nil {
		t.Errorf(`resolveCABundle("") = %q, %v; want none`, got, err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, "bundle.crt", newTestCA(t).pem)
	if got, err := resolveCABundle("bundle.crt"); err != nil || got != filepath.Join(dir, "bundle.crt") {
		t.Errorf("resolveCABundle(relative) = %q, %v; want %s", got, err, filepath.Join(dir, "bundle.crt"))
	}
	if _, err := resolveCABundle("missing.crt"); err == nil || !strings.Contains(err.Error(), "--ca-bundle") {
		t.Errorf("missing file: err = %v, want a --ca-bundle error", err)
	}
	writeTestFile(t, "empty.crt", []byte("not a certificate\n"))
	if _, err := resolveCABundle("empty.crt"); err == nil || !strings.Contains(err.Error(), "holds no PEM certificate") {
		t.Errorf("no certificate: err = %v, want it refused", err)
	}
}

func TestCABundleFlagIsValidated(t *testing.T) {
	bad := writeTestFile(t, filepath.Join(t.TempDir(), "bad.crt"), []byte("nothing"))
	for _, args := range [][]string{
		{"build", "--ca-bundle", bad},
		{"up", "--ca-bundle", bad},
		{"run-user-commands", "--ca-bundle", bad},
	} {
		var stderr bytes.Buffer
		root := NewRootCommand()
		root.SetArgs(append(args, "--workspace-folder", t.TempDir()))
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&stderr)
		root.SilenceErrors = true
		if err := root.Execute(); err == nil || !strings.Contains(stderr.String(), "--ca-bundle: "+bad+" holds no PEM certificate") {
			t.Errorf("%s: err %v, stderr %q; want the bundle refused", args[0], err, stderr.String())
		}
	}
}

func TestCABundleBuildSecrets(t *testing.T) {
	var logged bytes.Buffer
	logger := log.New(log.Options{Level: log.LevelTrace, Format: "text", Writer: &logged})
	if got := caBundleBuildSecrets("", false, logger); got != nil || logged.Len() != 0 {
		t.Errorf("no bundle: %v, logged %q; want nothing", got, logged.String())
	}
	if got := caBundleBuildSecrets("/b.crt", true, logger); !maps.Equal(got, map[string]string{"devcontainer-ca-bundle": "/b.crt"}) {
		t.Errorf("buildx: %v", got)
	}
	if got := caBundleBuildSecrets("/b.crt", false, logger); got != nil || !strings.Contains(logged.String(), "--ca-bundle requires BuildKit") {
		t.Errorf("legacy builder: %v, logged %q; want the bundle dropped with a warning", got, logged.String())
	}
}

func TestWithCABundleEnv(t *testing.T) {
	cfg := map[string]string{"SSL_CERT_FILE": "/etc/mine.pem", "FOO": "bar"}
	got := withCABundleEnv(cfg, "/b.crt")
	want := map[string]string{"FOO": "bar"}
	for _, kv := range imagemeta.CABundleEnv() {
		k, v, _ := strings.Cut(kv, "=")
		want[k] = v
	}
	if !maps.Equal(got, want) {
		t.Errorf("withCABundleEnv = %v, want %v", got, want)
	}
	if cfg["SSL_CERT_FILE"] != "/etc/mine.pem" || len(cfg) != 2 {
		t.Errorf("the configuration's env changed: %v", cfg)
	}
	if got := withCABundleEnv(nil, "/b.crt"); len(got) != 5 {
		t.Errorf("withCABundleEnv(nil) = %v, want the five variables", got)
	}
	if got := withCABundleEnv(cfg, ""); !maps.Equal(got, cfg) {
		t.Errorf("no bundle: %v, want %v", got, cfg)
	}
}

func TestCABundleEnvNamesEachTLSClient(t *testing.T) {
	want := []string{
		"SSL_CERT_FILE=/run/devcontainer/ca-bundle.crt",
		"NODE_EXTRA_CA_CERTS=/run/devcontainer/ca-bundle.crt",
		"REQUESTS_CA_BUNDLE=/run/devcontainer/ca-bundle.crt",
		"CURL_CA_BUNDLE=/run/devcontainer/ca-bundle.crt",
		"GIT_SSL_CAINFO=/run/devcontainer/ca-bundle.crt",
	}
	if got := imagemeta.CABundleEnv(); !slices.Equal(got, want) {
		t.Errorf("CABundleEnv = %v, want %v", got, want)
	}
	if got := caBundleRemoteEnv("/b.crt"); !slices.Equal(got, want) {
		t.Errorf("caBundleRemoteEnv = %v, want %v", got, want)
	}
	if got := caBundleRemoteEnv(""); got != nil {
		t.Errorf(`caBundleRemoteEnv("") = %v, want none`, got)
	}
}

func TestCABundleMountIsReadOnly(t *testing.T) {
	want := dockermount.Mount{Type: dockermount.TypeBind, Source: "/b.crt", Target: "/run/devcontainer/ca-bundle.crt", ReadOnly: true}
	if got := caBundleMount("/b.crt"); got != want {
		t.Errorf("caBundleMount = %+v, want %+v", got, want)
	}
}

func TestContainerHasCABundle(t *testing.T) {
	if containerHasCABundle([]container.MountPoint{{Destination: "/workspaces/x"}}) {
		t.Error("a container without the bundle's mount has it")
	}
	if !containerHasCABundle([]container.MountPoint{{Destination: "/workspaces/x"}, {Destination: "/run/devcontainer/ca-bundle.crt"}}) {
		t.Error("a container with the bundle's mount lacks it")
	}
}
