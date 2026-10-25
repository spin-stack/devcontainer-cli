package httpx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// childEnv makes the test binary act as a probe process: it builds a transport
// in a process whose system roots and GODEBUG the parent chose, and prints what
// the transport trusts. Both are read once per process, so only a fresh process
// can set them.
const childEnv = "HTTPX_TRANSPORT_PROBE"

type probe struct {
	NextProtos []string `json:"nextProtos"`
	// TrustsSystemAndExtra is true when RootCAs holds exactly the system roots
	// (SSL_CERT_FILE) plus the NODE_EXTRA_CA_CERTS bundle.
	TrustsSystemAndExtra bool `json:"trustsSystemAndExtra"`
}

// TestMain installs a generated CA as this process's system roots
// (SSL_CERT_FILE, with an empty SSL_CERT_DIR), so tests that compare against
// the system pool do not depend on the host's trust store.
func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		os.Exit(probeTransport())
	}
	dir, err := os.MkdirTemp("", "httpx-roots-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code, err := runWithSystemRoots(m, dir)
	os.RemoveAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(code)
}

func runWithSystemRoots(m *testing.M, dir string) (int, error) {
	certDir := filepath.Join(dir, "certs")
	if err := os.Mkdir(certDir, 0o700); err != nil {
		return 0, err
	}
	roots, err := writeCA(dir, "system-root")
	if err != nil {
		return 0, err
	}
	os.Setenv("SSL_CERT_FILE", roots)
	os.Setenv("SSL_CERT_DIR", certDir)
	// Load the system roots now, while SSL_CERT_FILE names them: the pool is
	// read once per process, and tests later clear SSL_CERT_FILE.
	if _, err := x509.SystemCertPool(); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// probeTransport is the probe process: it reports what NewTransport trusts.
func probeTransport() int {
	tr := NewTransport()
	var p probe
	if cfg := tr.TLSClientConfig; cfg != nil {
		p.NextProtos = cfg.NextProtos
		want, err := poolOf(os.Getenv("SSL_CERT_FILE"), os.Getenv("NODE_EXTRA_CA_CERTS"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "probe:", err)
			return 3
		}
		p.TrustsSystemAndExtra = cfg.RootCAs != nil && cfg.RootCAs.Equal(want)
	}
	if err := json.NewEncoder(os.Stdout).Encode(p); err != nil {
		return 3
	}
	return 0
}

func poolOf(files ...string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no certificate in %s", f)
		}
	}
	return pool, nil
}

func writeCA(dir, name string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".pem")
	return path, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
}

func testCA(t *testing.T, name string) string {
	t.Helper()
	path, err := writeCA(t.TempDir(), name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func runProbe(t *testing.T, godebug string) probe {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	// SSL_CERT_FILE and SSL_CERT_DIR come from TestMain through os.Environ.
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		"GODEBUG="+godebug,
		"NODE_EXTRA_CA_CERTS="+testCA(t, "corporate-proxy"),
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe process failed: %v\n%s", err, out)
	}
	var p probe
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	return p
}

// An extra CA is added to the system roots, not substituted for them (behind a
// TLS-intercepting proxy some hosts are still reached directly), and the rest
// of the default TLS config, HTTP/2 negotiation included, is kept.
func TestNewTransportKeepsSystemRootsAndHTTP2WithExtraCA(t *testing.T) {
	extra := testCA(t, "corporate-proxy")
	t.Setenv("NODE_EXTRA_CA_CERTS", extra)
	base := http.DefaultTransport.(*http.Transport).Clone().TLSClientConfig
	if base == nil {
		t.Fatal("the default transport has no TLS config; is GODEBUG=http2client=0 set?")
	}

	cfg := NewTransport().TLSClientConfig
	want, err := poolOf(os.Getenv("SSL_CERT_FILE"), extra)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || !cfg.RootCAs.Equal(want) {
		t.Fatal("RootCAs must hold the system roots plus NODE_EXTRA_CA_CERTS")
	}
	if !slices.Equal(cfg.NextProtos, base.NextProtos) {
		t.Errorf("NextProtos = %v, want the default %v", cfg.NextProtos, base.NextProtos)
	}
	if http.DefaultTransport.(*http.Transport).TLSClientConfig.RootCAs != nil {
		t.Error("NewTransport must not change the default transport's TLS config")
	}
}

// With HTTP/2 disabled the cloned transport has no TLS config; the extra CA must
// still be installed on a fresh one.
func TestNewTransportInstallsExtraCAWithoutATLSConfig(t *testing.T) {
	p := runProbe(t, "http2client=0")
	if !p.TrustsSystemAndExtra {
		t.Error("RootCAs must hold the system roots plus NODE_EXTRA_CA_CERTS")
	}
	if len(p.NextProtos) != 0 {
		t.Errorf("NextProtos = %v, want none with http2client=0", p.NextProtos)
	}
}

func TestLoadCACertsAddsTheBundleToTheSystemRoots(t *testing.T) {
	extra := testCA(t, "corporate-proxy")
	got, err := loadCACerts(extra)
	if err != nil {
		t.Fatal(err)
	}
	want, err := poolOf(os.Getenv("SSL_CERT_FILE"), extra)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatal("loadCACerts must return the system roots plus the bundle")
	}
}

func TestLoadCACertsReportsAnUnreadableBundle(t *testing.T) {
	pool, err := loadCACerts(filepath.Join(t.TempDir(), "missing.pem"))
	if !errors.Is(err, fs.ErrNotExist) || pool != nil {
		t.Fatalf("loadCACerts(missing) = %v, %v; want nil, ErrNotExist", pool, err)
	}
}
