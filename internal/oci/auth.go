package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
)

// credential holds resolved auth for a registry.
type credential struct {
	base64Encoded string // base64(user:token) for Basic auth
	refreshToken  string // OAuth refresh token (ACR identitytoken)
}

// getCredential resolves credentials for a registry.
// Order: DEVCONTAINERS_OCI_AUTH → docker config/credHelper → GITHUB_TOKEN → platform default → anonymous.
func getCredential(env map[string]string, registry string, logger log.Logger) *credential {
	// 1. DEVCONTAINERS_OCI_AUTH env var
	if ociAuth := env["DEVCONTAINERS_OCI_AUTH"]; ociAuth != "" {
		for _, entry := range strings.Split(ociAuth, ",") {
			parts := strings.SplitN(entry, "|", 3)
			if len(parts) == 3 && parts[0] == registry {
				logger.Write(fmt.Sprintf("[httpOci] Using match from DEVCONTAINERS_OCI_AUTH for registry %q", registry), log.LevelTrace)
				userToken := parts[1] + ":" + parts[2]
				return &credential{base64Encoded: base64.StdEncoding.EncodeToString([]byte(userToken))}
			}
		}
	}

	// 2. Docker config / credential helpers
	if cred := getCredentialFromDockerConfig(env, registry, logger); cred != nil {
		return cred
	}

	// 3. GITHUB_TOKEN for ghcr.io
	githubToken := env["GITHUB_TOKEN"]
	githubHost := env["GITHUB_HOST"]
	if registry == "ghcr.io" && githubToken != "" && (githubHost == "" || githubHost == "github.com") {
		logger.Write("[httpOci] Using environment GITHUB_TOKEN for auth", log.LevelTrace)
		userToken := "USERNAME:" + githubToken
		return &credential{base64Encoded: base64.StdEncoding.EncodeToString([]byte(userToken))}
	}

	// 4. Anonymous
	logger.Write(fmt.Sprintf("[httpOci] No credentials found for registry %q. Accessing anonymously.", registry), log.LevelTrace)
	return nil
}

// dockerConfigFile represents ~/.docker/config.json
type dockerConfigFile struct {
	Auths       map[string]dockerConfigAuth `json:"auths"`
	CredHelpers map[string]string           `json:"credHelpers"`
	CredsStore  string                      `json:"credsStore"`
}

type dockerConfigAuth struct {
	Auth          string `json:"auth,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
}

func getCredentialFromDockerConfig(env map[string]string, registry string, logger log.Logger) *credential {
	configPath := env["DOCKER_CONFIG"]
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".docker")
	}
	configFile := filepath.Join(configPath, "config.json")

	data, err := os.ReadFile(configFile)
	if err != nil {
		return tryPlatformDefaultHelper(registry, logger)
	}

	var config dockerConfigFile
	if err := jsonc.Unmarshal(data, &config); err != nil {
		logger.Write(fmt.Sprintf("[httpOci] Failed to parse docker config: %v", err), log.LevelTrace)
		return nil
	}

	hasAuth := len(config.CredHelpers) > 0 || config.CredsStore != "" || len(config.Auths) > 0

	// credHelpers for specific registry
	if helper, ok := config.CredHelpers[registry]; ok {
		logger.Write(fmt.Sprintf("[httpOci] Found credential helper %q for registry %q", helper, registry), log.LevelTrace)
		if cred := getCredentialFromHelper(registry, helper, logger); cred != nil {
			return cred
		}
	}

	// credsStore (global helper)
	if config.CredsStore != "" {
		logger.Write(fmt.Sprintf("[httpOci] Invoking credsStore credential helper %q", config.CredsStore), log.LevelTrace)
		if cred := getCredentialFromHelper(registry, config.CredsStore, logger); cred != nil {
			return cred
		}
	}

	// auths (static credentials)
	if auth, ok := config.Auths[registry]; ok {
		logger.Write(fmt.Sprintf("[httpOci] Found auths entry for registry %q", registry), log.LevelTrace)
		if auth.IdentityToken != "" {
			return &credential{refreshToken: auth.IdentityToken}
		}
		return &credential{base64Encoded: auth.Auth}
	}

	if !hasAuth {
		return tryPlatformDefaultHelper(registry, logger)
	}

	return nil
}

func tryPlatformDefaultHelper(registry string, logger log.Logger) *credential {
	helper := defaultCredentialHelperName()
	if helper == "" {
		return nil
	}
	logger.Write(fmt.Sprintf("[httpOci] Trying platform default credential helper %q", helper), log.LevelTrace)
	return getCredentialFromHelper(registry, helper, logger)
}

// defaultCredentialHelperName returns the docker credential helper the current
// platform defaults to (i.e. the suffix of docker-credential-<name>). Linux is
// the only supported target; the darwin/windows names are provided for
// completeness but are not exercised here.
//
// On Linux the libsecret-backed helper is `secretservice`
// (docker-credential-secretservice), not `secret` — there is no
// docker-credential-secret binary.
func defaultCredentialHelperName() string {
	switch runtime.GOOS {
	case "darwin":
		return "osxkeychain"
	case "windows":
		return "wincred"
	case "linux":
		return linuxDefaultHelperName(pathExists("pass"))
	}
	return ""
}

// linuxDefaultHelperName picks the Linux default helper: `pass` when the pass
// store is installed, otherwise the libsecret-backed `secretservice`.
func linuxDefaultHelperName(passAvailable bool) string {
	if passAvailable {
		return "pass"
	}
	return "secretservice"
}

type credHelperResult struct {
	Username string `json:"Username"`
	Secret   string `json:"Secret"`
}

func getCredentialFromHelper(registry, helperName string, logger log.Logger) *credential {
	// Bound the helper: a stuck docker-credential-* (locked keyring,
	// docker-credential-desktop waiting on a GUI, ...) must not block the whole
	// feature/image pull forever — time out and fall through to other auth.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker-credential-"+helperName, "get")
	cmd.Stdin = strings.NewReader(registry)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Write(fmt.Sprintf("[httpOci] Credential helper %q timed out after 15s for %q; skipping it (falling back to GITHUB_TOKEN / anonymous).", helperName, registry), log.LevelWarning)
		} else {
			logger.Write(fmt.Sprintf("[httpOci] Credential helper %q failed for %q: %v", helperName, registry, err), log.LevelTrace)
		}
		return nil
	}
	if len(output) == 0 {
		return nil
	}

	var creds credHelperResult
	if err := json.Unmarshal(output, &creds); err != nil {
		logger.Write(fmt.Sprintf("[httpOci] Credential helper %q returned non-JSON for %q", helperName, registry), log.LevelWarning)
		return nil
	}

	if creds.Username == "<token>" {
		return &credential{refreshToken: creds.Secret}
	}
	userToken := creds.Username + ":" + creds.Secret
	return &credential{base64Encoded: base64.StdEncoding.EncodeToString([]byte(userToken))}
}

func pathExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
