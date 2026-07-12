// Package config loads, parses and variable-substitutes devcontainer.json configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/pfs"
)

// NotFoundError is returned when no devcontainer.json can be discovered.
// It is distinguished from parse/other errors so callers (e.g. read-configuration)
// can mirror the TS CLI, which exits 1 silently when config is absent.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("Dev container config (%s) not found.", e.Path)
}

// LoadResult contains a loaded and substituted devcontainer configuration.
type LoadResult struct {
	Config          *DevContainer
	Raw             map[string]interface{} // pre-substitution
	WorkspaceConfig *WorkspaceConfig
	// HostSub is the host-side substitution context used for the config; reuse it
	// so Feature metadata (mounts/containerEnv) resolves ${localEnv:…} the same way.
	HostSub HostSubContext
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
		return nil, &NotFoundError{Path: defaultPath}
	}

	// Read the base config and (when given) deep-merge the override on top.
	//
	// NOTE: deliberate Go divergence. TS replaces the config wholesale with the
	// override file (readDocument(overrideConfigFile ?? configFile)); Go deep-merges
	// so an orchestrator can supply a partial override without restating the whole
	// devcontainer.json. When there is no readable base, the override stands alone
	// (identical to the TS/replace behavior).
	var raw map[string]interface{}
	if configPath != "" {
		if baseData, berr := os.ReadFile(configPath); berr == nil {
			if perr := jsonc.Unmarshal(baseData, &raw); perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
		}
	}
	if overrideConfigPath != "" {
		overrideData, oerr := os.ReadFile(overrideConfigPath)
		if oerr != nil {
			return nil, fmt.Errorf("read override config %s: %w", overrideConfigPath, oerr)
		}
		var overrideRaw map[string]interface{}
		if perr := jsonc.Unmarshal(overrideData, &overrideRaw); perr != nil {
			return nil, fmt.Errorf("parse override config %s: %w", overrideConfigPath, perr)
		}
		raw = deepMergeConfig(raw, overrideRaw)
	}
	if raw == nil {
		// No override and the base could not be read: surface the base read error.
		readPath := configPath
		if overrideConfigPath != "" {
			readPath = overrideConfigPath
		}
		data, err := os.ReadFile(readPath)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", readPath, err)
		}
		if err := jsonc.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", readPath, err)
		}
	}

	// Derive the typed struct from the (possibly merged) raw config.
	var config DevContainer
	if err := remarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
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
		HostSub:         ctx,
	}, nil
}

// deepMergeConfig deep-merges the override map onto the base map: nested objects
// are merged recursively; scalars and arrays in the override replace the base.
// A nil base returns the override (and vice-versa), so it composes cleanly when
// only one side is present.
func deepMergeConfig(base, override map[string]interface{}) map[string]interface{} {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	out := make(map[string]interface{}, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range override {
		if bv, ok := out[k]; ok {
			if bm, bok := bv.(map[string]interface{}); bok {
				if om, ook := ov.(map[string]interface{}); ook {
					out[k] = deepMergeConfig(bm, om)
					continue
				}
			}
		}
		out[k] = ov
	}
	return out
}

// DockerComposeFilePaths resolves the docker-compose file paths.
// Follows the TS fallback chain: config → COMPOSE_FILE env → .env file → defaults.
func DockerComposeFilePaths(config *DevContainer, env map[string]string, cwd string) ([]string, error) {
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

func computeWorkspaceConfig(workspace *Workspace, config *DevContainer, mountWorkspaceGitRoot bool) *WorkspaceConfig {
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
		// 0.88 only appends consistency on non-Linux hosts (macOS/Windows);
		// on Linux the bind mount carries no consistency= suffix (getWorkspaceConfiguration).
		consistency := ""
		if runtime.GOOS != "linux" {
			consistency = ",consistency=consistent"
		}
		wc.WorkspaceMount = fmt.Sprintf("type=bind,source=%s,target=/workspaces/%s%s", sourceFolder, filepath.Base(sourceFolder), consistency)
	}

	if config.WorkspaceFolder != "" {
		wc.WorkspaceFolder = config.WorkspaceFolder
	}
	if config.WorkspaceMount != "" {
		wc.WorkspaceMount = config.WorkspaceMount
	}
	return wc
}

// detectGitRoot finds the git working-tree root for the given path by walking up
// the directory tree looking for a `.git` entry — no `git` binary.
//
// The walk is purely lexical (via filepath.Dir), matching git's `--show-cdup`
// rather than `--show-toplevel`: it does NOT canonicalize symlinks (on macOS
// `/tmp` → `/private/tmp`), which would desync sourceFolder from
// workspace.RootFolderPath and produce a corrupt workspaceFolder (e.g.
// `/workspaces/x/../../..`) and a different mount. Returns "" when dir is not
// inside a repository.
func detectGitRoot(dir string) string {
	dir = filepath.Clean(dir)
	for {
		// `.git` is a directory in a normal clone and a regular file (a gitlink)
		// in a worktree or submodule checkout — accept either.
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached the filesystem root without finding a repo
		}
		dir = parent
	}
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

func remarshal(raw map[string]interface{}, target *DevContainer) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return jsonc.Unmarshal(data, target)
}
