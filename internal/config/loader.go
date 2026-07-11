package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devcontainers/cli/internal/core/jsonc"
	"github.com/devcontainers/cli/internal/core/pfs"
)

// LoadResult contains a loaded and substituted devcontainer configuration.
type LoadResult struct {
	Config          *DevContainerConfig
	Raw             map[string]interface{} // pre-substitution
	WorkspaceConfig *WorkspaceConfig
}

// WorkspaceConfig describes the workspace mount and folder inside the container.
type WorkspaceConfig struct {
	WorkspaceFolder string `json:"workspaceFolder"`
	WorkspaceMount  string `json:"workspaceMount,omitempty"`
}

// Workspace describes the local workspace.
type Workspace struct {
	IsWorkspaceFile       bool
	WorkspaceOrFolderPath string
	RootFolderPath        string
	ConfigFolderPath      string
}

// WorkspaceFromPath creates a Workspace from a local folder path.
func WorkspaceFromPath(folderPath string) *Workspace {
	ext := filepath.Ext(folderPath)
	if ext == ".code-workspace" {
		dir := filepath.Dir(folderPath)
		return &Workspace{
			IsWorkspaceFile:       true,
			WorkspaceOrFolderPath: folderPath,
			RootFolderPath:        dir,
			ConfigFolderPath:      dir,
		}
	}
	return &Workspace{
		IsWorkspaceFile:       false,
		WorkspaceOrFolderPath: folderPath,
		RootFolderPath:        folderPath,
		ConfigFolderPath:      folderPath,
	}
}

// FindConfigFile discovers the devcontainer.json path using the standard search order:
// 1. .devcontainer/devcontainer.json
// 2. .devcontainer.json
func FindConfigFile(configFolderPath string) string {
	candidates := []string{
		filepath.Join(configFolderPath, ".devcontainer", "devcontainer.json"),
		filepath.Join(configFolderPath, ".devcontainer.json"),
	}
	for _, c := range candidates {
		if pfs.IsFile(c) {
			return c
		}
	}
	return ""
}

// LoadDevContainerConfig loads and parses a devcontainer.json file.
// If configPath is empty, it discovers the config file from workspaceFolder.
func LoadDevContainerConfig(workspaceFolder, configPath, overrideConfigPath string) (*LoadResult, error) {
	workspace := WorkspaceFromPath(workspaceFolder)

	// Discover config path
	if configPath == "" {
		configPath = FindConfigFile(workspace.ConfigFolderPath)
	}
	if configPath == "" && overrideConfigPath == "" {
		defaultPath := filepath.Join(workspaceFolder, ".devcontainer", "devcontainer.json")
		return nil, fmt.Errorf("Dev container config (%s) not found.", defaultPath)
	}

	// Read from override or primary
	readPath := configPath
	if overrideConfigPath != "" {
		readPath = overrideConfigPath
	}

	data, err := os.ReadFile(readPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", readPath, err)
	}

	// Parse JSONC
	var raw map[string]interface{}
	if err := jsonc.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", readPath, err)
	}

	// Unmarshal into typed struct
	var config DevContainerConfig
	if err := jsonc.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal config %s: %w", readPath, err)
	}

	// Migrate deprecated properties
	UpdateFromOldProperties(&config)

	// Set config file path
	if configPath != "" {
		config.ConfigFilePath = configPath
	} else {
		config.ConfigFilePath = overrideConfigPath
	}

	// Compute workspace config
	wsConfig := computeWorkspaceConfig(workspace, &config, true)

	// Apply host-side variable substitution
	// Trim trailing slash from paths to avoid double-slash in substitution
	localWS := strings.TrimRight(workspace.RootFolderPath, "/\\")
	ctx := HostSubContext{
		Platform:                 currentPlatform(),
		LocalWorkspaceFolder:     localWS,
		ContainerWorkspaceFolder: wsConfig.WorkspaceFolder,
		Env:                      envFromOS(),
		ConfigFilePath:           config.ConfigFilePath,
	}

	substituted := SubstituteHost(ctx, raw)
	if m, ok := substituted.(map[string]interface{}); ok {
		// Re-unmarshal the substituted raw into the typed config
		subData, _ := jsonc.StripComments(data)
		_ = subData // keep original data for raw
		raw = m
	}

	// Re-unmarshal substituted values into the config struct
	// We do this by marshaling the substituted raw and unmarshaling again
	// This ensures all string fields have variables resolved
	if err := remarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("apply substitutions: %w", err)
	}

	// Update workspace folder from config if specified
	if config.WorkspaceFolder != "" {
		wsConfig.WorkspaceFolder = config.WorkspaceFolder
	}
	if config.WorkspaceMount != "" {
		wsConfig.WorkspaceMount = config.WorkspaceMount
	}

	config.ConfigFilePath = configPath
	if configPath == "" {
		config.ConfigFilePath = overrideConfigPath
	}

	return &LoadResult{
		Config:          &config,
		Raw:             raw,
		WorkspaceConfig: wsConfig,
	}, nil
}

// GetDockerComposeFilePaths resolves the docker-compose file paths.
// Follows the TS fallback chain: config → COMPOSE_FILE env → .env file → defaults.
func GetDockerComposeFilePaths(config *DevContainerConfig, env map[string]string, cwd string) ([]string, error) {
	configDir := filepath.Dir(config.ConfigFilePath)

	// 1. From config property
	if len(config.DockerComposeFile) > 0 {
		paths := make([]string, len(config.DockerComposeFile))
		for i, f := range config.DockerComposeFile {
			paths[i] = pfs.Resolve(configDir, f)
		}
		return paths, nil
	}

	// 2. COMPOSE_FILE env var
	if composeFile, ok := env["COMPOSE_FILE"]; ok && composeFile != "" {
		sep := string(os.PathListSeparator)
		parts := strings.Split(composeFile, sep)
		paths := make([]string, len(parts))
		for i, p := range parts {
			paths[i] = pfs.Resolve(cwd, p)
		}
		return paths, nil
	}

	// 3. .env file
	envFilePath := filepath.Join(cwd, ".env")
	if pfs.IsFile(envFilePath) {
		data, err := os.ReadFile(envFilePath)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "COMPOSE_FILE=") {
					value := strings.TrimPrefix(line, "COMPOSE_FILE=")
					value = strings.TrimSpace(value)
					if value != "" {
						sep := string(os.PathListSeparator)
						parts := strings.Split(value, sep)
						paths := make([]string, len(parts))
						for i, p := range parts {
							paths[i] = pfs.Resolve(cwd, p)
						}
						return paths, nil
					}
				}
			}
		}
	}

	// 4. Defaults
	defaults := []string{pfs.Resolve(cwd, "docker-compose.yml")}
	override := pfs.Resolve(cwd, "docker-compose.override.yml")
	if pfs.IsFile(override) {
		defaults = append(defaults, override)
	}
	return defaults, nil
}

// --- Helpers ---

func computeWorkspaceConfig(workspace *Workspace, config *DevContainerConfig, mountWorkspaceGitRoot bool) *WorkspaceConfig {
	sourceFolder := workspace.RootFolderPath

	// Detect git root for workspace mounting (matches TS getHostMountFolder)
	if mountWorkspaceGitRoot {
		if gitRoot := detectGitRoot(sourceFolder); gitRoot != "" {
			sourceFolder = gitRoot
		}
	}

	containerFolder := "/workspaces/" + filepath.Base(sourceFolder)

	// If the workspace is inside a subfolder of the git root, adjust the container path
	if mountWorkspaceGitRoot && sourceFolder != workspace.RootFolderPath {
		rel, err := filepath.Rel(sourceFolder, workspace.RootFolderPath)
		if err == nil && rel != "." {
			containerFolder = "/workspaces/" + filepath.Base(sourceFolder) + "/" + rel
		}
	}

	wc := &WorkspaceConfig{
		WorkspaceFolder: containerFolder,
	}

	// Only compute workspaceMount for non-compose configs
	// Docker Compose manages its own mounts via the compose file
	if !config.IsComposeConfig() {
		wc.WorkspaceMount = fmt.Sprintf("type=bind,source=%s,target=/workspaces/%s,consistency=consistent", sourceFolder, filepath.Base(sourceFolder))
	}

	if config.WorkspaceFolder != "" {
		wc.WorkspaceFolder = config.WorkspaceFolder
	}
	if config.WorkspaceMount != "" {
		wc.WorkspaceMount = config.WorkspaceMount
	}
	return wc
}

// detectGitRoot finds the git repository root for the given path.
//
// It uses `--show-cdup` (a path relative to dir, up to the working-tree root)
// rather than `--show-toplevel`, matching the TS CLI. `--show-toplevel`
// canonicalizes symlinks (on macOS `/tmp` → `/private/tmp`), which desynced
// sourceFolder from workspace.RootFolderPath and produced a corrupt
// workspaceFolder (e.g. `/workspaces/x/../../..`) and a different mount.
func detectGitRoot(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-cdup")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	cdup := strings.TrimSpace(string(out))
	return filepath.Clean(filepath.Join(dir, cdup))
}

func currentPlatform() string {
	if IsWindows() {
		return "win32"
	}
	return os.Getenv("GOOS_OVERRIDE") // for testing; falls back to runtime
}

func envFromOS() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			env[e[:i]] = e[i+1:]
		}
	}
	return env
}

func remarshal(raw map[string]interface{}, target *DevContainerConfig) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return jsonc.Unmarshal(data, target)
}
