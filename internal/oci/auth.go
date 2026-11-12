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

type credential struct {
	base64Encoded string
	refreshToken  string
}

func getCredential(env map[string]string, registry string, logger log.Logger) *credential {
	if ociAuth := env["DEVCONTAINERS_OCI_AUTH"]; ociAuth != "" {
		for entry := range strings.SplitSeq(ociAuth, ",") {
			parts := strings.Split(entry, "|")
			if parts[0] != registry {
				continue
			}
			logger.Write(fmt.Sprintf("[httpOci] Using match from DEVCONTAINERS_OCI_AUTH for registry '%s'", registry), log.LevelTrace)
			// TS joins the second and third fields, ignoring any after them; a
			// missing field is JS undefined.
			parts = append(parts, "undefined", "undefined")
			userToken := parts[1] + ":" + parts[2]
			return &credential{base64Encoded: base64.StdEncoding.EncodeToString([]byte(userToken))}
		}
	}

	if cred := getCredentialFromDockerConfig(env, registry, logger); cred != nil {
		return cred
	}

	githubToken := env["GITHUB_TOKEN"]
	githubHost := env["GITHUB_HOST"]
	if registry == "ghcr.io" && githubToken != "" && (githubHost == "" || githubHost == "github.com") {
		logger.Write("[httpOci] Using environment GITHUB_TOKEN for auth", log.LevelTrace)
		userToken := "USERNAME:" + githubToken
		return &credential{base64Encoded: base64.StdEncoding.EncodeToString([]byte(userToken))}
	}

	logger.Write(fmt.Sprintf("[httpOci] No credentials found for registry %q. Accessing anonymously.", registry), log.LevelTrace)
	return nil
}

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

	if helper := config.CredHelpers[registry]; helper != "" {
		logger.Write(fmt.Sprintf("[httpOci] Found credential helper %q for registry %q", helper, registry), log.LevelTrace)
		if cred := getCredentialFromHelper(registry, helper, logger); cred != nil {
			return cred
		}
	} else if config.CredsStore != "" {
		logger.Write(fmt.Sprintf("[httpOci] Invoking credsStore credential helper %q", config.CredsStore), log.LevelTrace)
		if cred := getCredentialFromHelper(registry, config.CredsStore, logger); cred != nil {
			return cred
		}
	}

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
