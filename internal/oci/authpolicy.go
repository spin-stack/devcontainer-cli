package oci

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/devcontainers/cli/internal/log"
)

// AuthDiagnostics mirrors the TS OCIAuthDiagnostics record (spec-common/ociAuth.ts).
// It reports what WOULD change if `--oci-auth-hardening` were enabled, so a user
// can measure the compatibility impact before turning the hardening on. The JSON
// field names are part of the `up`/`build`/`read-configuration` output contract.
type AuthDiagnostics struct {
	AuthLookupWouldBeBlocked                         bool `json:"authLookupWouldBeBlocked"`
	RegistryRedirectWouldPreventCredentialForwarding bool `json:"registryRedirectWouldPreventCredentialForwarding"`
	AuthServerRedirect                               bool `json:"authServerRedirect"`
}

// builtInCrossOriginAuthHosts are the registry→auth-host mappings the reference
// CLI trusts out of the box (httpOCIRegistry.ts).
var builtInCrossOriginAuthHosts = []string{
	"registry-1.docker.io=auth.docker.io",
	"registry.docker.io=auth.docker.io",
	"docker.io=auth.docker.io",
	"index.docker.io=auth.docker.io",
	"registry.gitlab.com=gitlab.com",
}

// dockerHubRegistryHosts are the equivalent authorities Docker Hub references and
// distribution requests use interchangeably.
var dockerHubRegistryHosts = map[string]bool{
	"registry-1.docker.io": true,
	"registry.docker.io":   true,
	"docker.io":            true,
	"index.docker.io":      true,
}

// AuthPolicy carries the OCI authentication policy for one CLI invocation: the
// `--oci-auth-hardening` switch, the trusted cross-origin auth hosts, and the
// diagnostics accumulated while talking to registries. A single value is shared
// by every oci.Client a command builds, so the diagnostics reported in the
// command's JSON output cover all registry traffic of that command.
type AuthPolicy struct {
	hardening   bool
	crossOrigin map[string]map[string]bool
	logger      log.Logger

	mu   sync.Mutex
	diag AuthDiagnostics
}

// NewAuthPolicy builds the policy from the CLI flags. allowedCrossOriginAuthHosts
// entries are '<registry-host>=<auth-host>' pairs, validated the same way the
// reference CLI validates them.
func NewAuthPolicy(hardening bool, allowedCrossOriginAuthHosts []string, logger log.Logger) (*AuthPolicy, error) {
	hosts, err := ParseCrossOriginAuthHosts(append(append([]string{}, builtInCrossOriginAuthHosts...), allowedCrossOriginAuthHosts...))
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Null
	}
	return &AuthPolicy{hardening: hardening, crossOrigin: hosts, logger: logger}, nil
}

// DefaultAuthPolicy is the policy used when no flags were parsed (hardening off,
// only the built-in cross-origin mappings). Its diagnostics are discarded.
func DefaultAuthPolicy() *AuthPolicy {
	p, err := NewAuthPolicy(false, nil, log.Null)
	if err != nil {
		// The built-in entries are constants and always parse.
		panic(fmt.Sprintf("default OCI auth policy: %v", err))
	}
	return p
}

// SetLogger directs the diagnostic log lines at the command's logger. It is set
// once the command has built its logger, after flag parsing.
func (p *AuthPolicy) SetLogger(logger log.Logger) {
	if p == nil || logger == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = logger
}

// Hardening reports whether `--oci-auth-hardening` was requested.
func (p *AuthPolicy) Hardening() bool { return p != nil && p.hardening }

// Diagnostics returns a snapshot of the diagnostics recorded so far.
func (p *AuthPolicy) Diagnostics() AuthDiagnostics {
	if p == nil {
		return AuthDiagnostics{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.diag
}

// record flips a diagnostic once and logs the reason the first time, matching the
// reference CLI's `[httpOci] OCI auth diagnostics: ...` line.
func (p *AuthPolicy) record(field *bool, message string) {
	p.mu.Lock()
	already := *field
	if !already {
		*field = true
	}
	logger := p.logger
	p.mu.Unlock()
	if !already {
		logger.Write("[httpOci] OCI auth diagnostics: "+message, log.LevelInfo)
	}
}

// ParseCrossOriginAuthHosts parses '<registry-host>=<auth-host>' entries into a
// registry → auth-hosts map, rejecting anything that is not a bare authority.
func ParseCrossOriginAuthHosts(entries []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, entry := range entries {
		sep := strings.Index(entry, "=")
		if sep <= 0 || sep != strings.LastIndex(entry, "=") || sep == len(entry)-1 {
			return nil, fmt.Errorf("Invalid cross-origin auth host '%s'. Expected '<registry-host>=<auth-host>'.", entry)
		}
		registry, err := normalizeHTTPSAuthority(entry[:sep])
		if err != nil {
			return nil, err
		}
		authHost, err := normalizeHTTPSAuthority(entry[sep+1:])
		if err != nil {
			return nil, err
		}
		if out[registry] == nil {
			out[registry] = map[string]bool{}
		}
		out[registry][authHost] = true
	}
	return out, nil
}

// normalizeHTTPSAuthority validates that authority is a bare host[:port] and
// returns it lower-cased.
func normalizeHTTPSAuthority(authority string) (string, error) {
	invalid := fmt.Errorf("Invalid authority '%s'.", authority)
	parsed, err := url.Parse("https://" + authority)
	if err != nil || parsed.Host == "" {
		return "", invalid
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalid
	}
	if parsed.Host != authority && !strings.EqualFold(parsed.Host, authority) {
		return "", invalid
	}
	return strings.ToLower(parsed.Host), nil
}

// isRegistryOrigin reports whether u addresses the registry of ociRef, treating
// the interchangeable Docker Hub authorities as one origin.
func isRegistryOrigin(u, registry *url.URL) bool {
	if sameOrigin(u, registry) {
		return true
	}
	return strings.EqualFold(u.Scheme, "https") && strings.EqualFold(registry.Scheme, "https") &&
		dockerHubRegistryHosts[strings.ToLower(u.Host)] && dockerHubRegistryHosts[strings.ToLower(registry.Host)]
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(canonicalAuthority(a), canonicalAuthority(b))
}

// canonicalAuthority is host[:port] with the scheme's default port applied.
func canonicalAuthority(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return host + ":" + port
}

// allowsTokenServiceRealm reports whether the registry may direct a token request
// to realm: the same authority over HTTPS (or HTTP on localhost), or an HTTPS auth
// host explicitly mapped to that registry.
func (p *AuthPolicy) allowsTokenServiceRealm(realm, registry *url.URL) bool {
	if isAllowedSameAuthorityRealm(realm, registry) {
		return true
	}
	if !strings.EqualFold(realm.Scheme, "https") {
		return false
	}
	return p.crossOrigin[strings.ToLower(registry.Host)][strings.ToLower(realm.Host)]
}

func isAllowedSameAuthorityRealm(realm, registry *url.URL) bool {
	if !strings.EqualFold(realm.Host, registry.Host) {
		return false
	}
	return strings.EqualFold(realm.Scheme, "https") ||
		(strings.EqualFold(realm.Scheme, "http") && strings.EqualFold(realm.Hostname(), "localhost"))
}

// authPolicyTransport applies the OCI auth policy to one repository's traffic.
//
// oras-go already refuses to forward registry credentials to a challenge that
// arrives from another origin, so this layer adds what the reference CLI's
// hardening adds on top: bearer realms are pinned to the registry authority (or
// an explicitly trusted auth host), token endpoints may not redirect, and the
// three compatibility diagnostics are recorded whether or not hardening is on.
type authPolicyTransport struct {
	base     http.RoundTripper
	policy   *AuthPolicy
	registry *url.URL

	mu sync.Mutex
	// tokenEndpoints holds the scheme://host/path of every realm a challenge
	// pointed at, so a token request can be told apart from a registry request.
	tokenEndpoints map[string]bool
}

func newAuthPolicyTransport(base http.RoundTripper, policy *AuthPolicy, registryScheme, registryHost string) *authPolicyTransport {
	return &authPolicyTransport{
		base:           base,
		policy:         policy,
		registry:       &url.URL{Scheme: registryScheme, Host: registryHost},
		tokenEndpoints: map[string]bool{},
	}
}

func endpointKey(u *url.URL) string {
	return strings.ToLower(u.Scheme+"://"+u.Host) + u.Path
}

func (t *authPolicyTransport) isTokenEndpoint(u *url.URL) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tokenEndpoints[endpointKey(u)]
}

func (t *authPolicyTransport) addTokenEndpoint(u *url.URL) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tokenEndpoints[endpointKey(u)] = true
}

func (t *authPolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tokenRequest := t.isTokenEndpoint(req.URL)

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	if tokenRequest {
		return t.inspectTokenResponse(req, resp)
	}
	return t.inspectRegistryResponse(req, resp)
}

// inspectTokenResponse enforces the "token endpoints must not redirect around
// their validated authority boundary" rule and records the redirect diagnostic.
func (t *authPolicyTransport) inspectTokenResponse(req *http.Request, resp *http.Response) (*http.Response, error) {
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return resp, nil
	}
	location, locErr := resp.Location()
	if locErr != nil {
		return resp, nil
	}
	if t.policy.Hardening() {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to request bearer token for %q: authentication server redirected the token request to %q", req.URL.Host, location.Host)
	}
	where := fmt.Sprintf("from origin '%s' to '%s'", originOf(req.URL), originOf(location))
	if sameOrigin(req.URL, location) {
		where = fmt.Sprintf("within origin '%s'", originOf(req.URL))
	}
	t.policy.record(&t.policy.diag.AuthServerRedirect, "Authentication server redirected a token request "+where+".")
	// The redirect target serves the same token request; keep treating it as one.
	t.addTokenEndpoint(location)
	return resp, nil
}

// inspectRegistryResponse records the credential-forwarding diagnostic and
// validates the bearer realm of an authentication challenge.
func (t *authPolicyTransport) inspectRegistryResponse(req *http.Request, resp *http.Response) (*http.Response, error) {
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return resp, nil
	}

	if !isRegistryOrigin(req.URL, t.registry) {
		t.policy.record(&t.policy.diag.RegistryRedirectWouldPreventCredentialForwarding,
			fmt.Sprintf("Request to '%s' with authentication challenge from '%s' would prevent forwarding registry '%s' credentials with OCI auth hardening.",
				req.URL.Host, req.URL.Host, t.registry.Host))
	}

	realm := bearerRealm(resp.Header.Get("WWW-Authenticate"))
	if realm == "" {
		return resp, nil
	}
	realmURL, err := url.Parse(realm)
	if err != nil || realmURL.Host == "" {
		if t.policy.Hardening() {
			resp.Body.Close()
			return nil, fmt.Errorf("registry '%s' requested authentication from an unparsable realm '%s'", req.URL.Host, realm)
		}
		return resp, nil
	}

	if t.policy.allowsTokenServiceRealm(realmURL, req.URL) {
		t.addTokenEndpoint(realmURL)
		return resp, nil
	}

	t.policy.record(&t.policy.diag.AuthLookupWouldBeBlocked,
		fmt.Sprintf("Authentication lookup from registry '%s' to realm origin '%s' would be blocked by OCI auth hardening.", req.URL.Host, originOf(realmURL)))
	if !t.policy.Hardening() {
		t.addTokenEndpoint(realmURL)
		return resp, nil
	}

	hint := ""
	if strings.EqualFold(realmURL.Scheme, "https") {
		hint = fmt.Sprintf(" Use '--allow-cross-origin-auth-host %s=%s' to trust this registry-to-auth-host mapping.", req.URL.Host, realmURL.Host)
	}
	resp.Body.Close()
	return nil, fmt.Errorf("registry '%s' requested authentication from untrusted realm '%s'.%s", req.URL.Host, realm, hint)
}

func originOf(u *url.URL) string {
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// bearerRealm extracts realm="..." from a Bearer WWW-Authenticate challenge.
func bearerRealm(challenge string) string {
	if challenge == "" {
		return ""
	}
	scheme, params, found := strings.Cut(strings.TrimSpace(challenge), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	for _, part := range strings.Split(params, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "realm") {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"`)
	}
	return ""
}
