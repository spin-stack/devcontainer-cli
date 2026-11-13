package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/pfs"
)

type NotObjectError struct {
	Path string
}

func (e *NotObjectError) Error() string {
	return fmt.Sprintf("Dev container config (%s) must contain a JSON object literal.", e.Path)
}

func wrapParseError(what, path string, err error) error {
	if _, ok := errors.AsType[*NotObjectError](err); ok {
		return err
	}
	return fmt.Errorf("%s %s: %w", what, path, err)
}

func parseConfigObject(data []byte, path string) (map[string]any, error) {
	var v any
	if err := jsonc.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, &NotObjectError{Path: path}
	}
	return m, nil
}

type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("Dev container config (%s) not found.", e.Path)
}

type LoadResult struct {
	Config          *DevContainer
	Raw             map[string]any
	RawConfig       map[string]any
	WorkspaceConfig *WorkspaceConfig
	HostSub         HostSubContext
	KeyOrder        []string
}

type WorkspaceConfig struct {
	WorkspaceFolder  string   `json:"workspaceFolder"`
	WorkspaceMount   string   `json:"workspaceMount,omitempty"`
	AdditionalMounts []string `json:"-"`
	NoWorkspaceMount bool     `json:"-"`
}

type Workspace struct {
	IsWorkspaceFile       bool
	WorkspaceOrFolderPath string
	RootFolderPath        string
	ConfigFolderPath      string
}

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

type MountOptions struct {
	MountWorkspaceGitRoot     bool
	MountGitWorktreeCommonDir bool
}

func LoadDevContainerConfig(workspaceFolder, configPath, overrideConfigPath string) (*LoadResult, error) {
	return LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overrideConfigPath, MountOptions{MountWorkspaceGitRoot: true})
}

func LoadDevContainerConfigWithMounts(workspaceFolder, configPath, overrideConfigPath string, mounts MountOptions) (*LoadResult, error) {
	workspace := WorkspaceFromPath(workspaceFolder)

	if configPath == "" && (workspaceFolder != "" || overrideConfigPath == "") {
		configPath = FindConfigFile(workspace.ConfigFolderPath)
		if configPath == "" && overrideConfigPath != "" {
			configPath = filepath.Join(workspace.ConfigFolderPath, ".devcontainer", "devcontainer.json")
		}
	}
	if configPath == "" && overrideConfigPath == "" {
		defaultPath := filepath.Join(workspaceFolder, ".devcontainer", "devcontainer.json")
		return nil, &NotFoundError{Path: defaultPath}
	}

	objectPath := cmp.Or(configPath, overrideConfigPath)
	var raw map[string]any
	var keyOrder []string
	var docs [][]byte
	if configPath != "" {
		baseData, err := os.ReadFile(configPath)
		switch {
		case err == nil:
			if raw, err = parseConfigObject(baseData, objectPath); err != nil {
				return nil, wrapParseError("parse config", configPath, err)
			}
			keyOrder = jsonc.Names(baseData)
			docs = append(docs, baseData)
		case overrideConfigPath == "":
			return nil, fmt.Errorf("read config %s: %w", configPath, err)
		}
	}
	if overrideConfigPath != "" {
		overrideData, oerr := os.ReadFile(overrideConfigPath)
		if errors.Is(oerr, fs.ErrNotExist) {
			return nil, &NotFoundError{Path: objectPath}
		}
		if oerr != nil {
			return nil, fmt.Errorf("read override config %s: %w", overrideConfigPath, oerr)
		}
		overrideRaw, perr := parseConfigObject(overrideData, objectPath)
		if perr != nil {
			return nil, wrapParseError("parse override config", overrideConfigPath, perr)
		}
		raw = deepMergeConfig(raw, overrideRaw)
		keyOrder = append(keyOrder, jsonc.Names(overrideData)...)
		docs = append(docs, overrideData)
	}

	raw = updateFromOldProperties(raw)
	rawConfig := raw

	var config DevContainer
	if err := remarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	configFilePath := configPath
	if configFilePath == "" {
		configFilePath = overrideConfigPath
	}

	_, config.workspaceMountSet = raw["workspaceMount"]

	wsConfig := computeWorkspaceConfig(workspace, &config, mounts.MountWorkspaceGitRoot, mounts.MountGitWorktreeCommonDir)

	localWS := strings.TrimRight(workspace.RootFolderPath, "/\\")
	ctx := HostSubContext{
		Platform:                 currentPlatform(),
		LocalWorkspaceFolder:     localWS,
		ContainerWorkspaceFolder: wsConfig.WorkspaceFolder,
		Env:                      EnvMap(os.Environ()),
		ConfigFilePath:           configFilePath,
	}

	substituted, err := NewVariableResolver().Resolve(SubstitutionContext{HostSubContext: ctx}, PhaseHost, raw)
	if err != nil {
		return nil, err
	}
	if m, ok := substituted.(map[string]any); ok {
		raw = m
	}

	if err := remarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("apply substitutions: %w", err)
	}

	if config.WorkspaceFolder != "" {
		wsConfig.WorkspaceFolder = config.WorkspaceFolder
	}
	if config.workspaceMountSet {
		wsConfig.WorkspaceMount = config.WorkspaceMount
		wsConfig.NoWorkspaceMount = config.WorkspaceMount == ""
	}

	config.ConfigFilePath = configFilePath
	config.ContainerEnvOrder, config.FeatureOrder, config.FeatureOptionOrder, config.BuildArgsOrder = declaredOrder(docs)

	return &LoadResult{
		Config:          &config,
		Raw:             raw,
		RawConfig:       rawConfig,
		WorkspaceConfig: wsConfig,
		HostSub:         ctx,
		KeyOrder:        keyOrder,
	}, nil
}

func declaredOrder(docs [][]byte) (containerEnv, featureIDs []string, featureOptions map[string][]string, buildArgs []string) {
	for _, data := range docs {
		containerEnv = append(containerEnv, jsonc.MemberNames(data, "containerEnv")...)
		buildArgs = append(buildArgs, jsonc.MemberNames(data, "build", "args")...)
		ids := jsonc.MemberNames(data, "features")
		featureIDs = append(featureIDs, ids...)
		for _, id := range ids {
			if names := jsonc.MemberNames(data, "features", id); len(names) > 0 {
				if featureOptions == nil {
					featureOptions = map[string][]string{}
				}
				featureOptions[id] = append(featureOptions[id], names...)
			}
		}
	}
	return containerEnv, features.KeyOrder(featureIDs), featureOptions, buildArgs
}

func deepMergeConfig(base, override map[string]any) map[string]any {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	out := make(map[string]any, len(base))
	maps.Copy(out, base)
	for k, ov := range override {
		if bv, ok := out[k]; ok {
			if bm, bok := bv.(map[string]any); bok {
				if om, ook := ov.(map[string]any); ook {
					out[k] = deepMergeConfig(bm, om)
					continue
				}
			}
		}
		out[k] = ov
	}
	return out
}

func DockerComposeFilePaths(config *DevContainer, env map[string]string, cwd string) ([]string, error) {
	configDir := filepath.Dir(config.ConfigFilePath)

	if len(config.DockerComposeFile) > 0 {
		paths := make([]string, len(config.DockerComposeFile))
		for i, f := range config.DockerComposeFile {
			paths[i] = pfs.Resolve(configDir, f)
		}
		return paths, nil
	}

	if composeFile, ok := env["COMPOSE_FILE"]; ok && composeFile != "" {
		sep := string(os.PathListSeparator)
		parts := strings.Split(composeFile, sep)
		paths := make([]string, len(parts))
		for i, p := range parts {
			paths[i] = pfs.Resolve(cwd, p)
		}
		return paths, nil
	}

	envFilePath := filepath.Join(cwd, ".env")
	if pfs.IsFile(envFilePath) {
		data, err := os.ReadFile(envFilePath)
		if err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
				line = strings.TrimSpace(line)
				if after, ok := strings.CutPrefix(line, "COMPOSE_FILE="); ok {
					value := after
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

	defaults := []string{pfs.Resolve(cwd, "docker-compose.yml")}
	override := pfs.Resolve(cwd, "docker-compose.override.yml")
	if pfs.IsFile(override) {
		defaults = append(defaults, override)
	}
	return defaults, nil
}

func computeWorkspaceConfig(workspace *Workspace, config *DevContainer, mountWorkspaceGitRoot, mountGitWorktreeCommonDir bool) *WorkspaceConfig {
	if config.IsComposeConfig() {
		folder := config.WorkspaceFolder
		if folder == "" {
			folder = "/"
		}
		return &WorkspaceConfig{WorkspaceFolder: folder}
	}
	if workspace.RootFolderPath == "" {
		return &WorkspaceConfig{WorkspaceFolder: config.WorkspaceFolder, WorkspaceMount: config.WorkspaceMount}
	}
	sourceFolder := workspace.RootFolderPath

	if mountWorkspaceGitRoot {
		if gitRoot := detectGitRoot(sourceFolder); gitRoot != "" {
			sourceFolder = gitRoot
		}
	}

	consistency := ""
	if runtime.GOOS != "linux" {
		consistency = ",consistency=consistent"
	}

	containerMountFolder := "/workspaces/" + filepath.Base(sourceFolder)
	var additionalMounts []string
	hasWorkspaceMount := config.WorkspaceMount != "" || config.workspaceMountSet
	computed := config.WorkspaceFolder == "" || !hasWorkspaceMount
	if computed && mountWorkspaceGitRoot && mountGitWorktreeCommonDir {
		if remapped, commonDirMount, ok := gitWorktreeCommonDirMount(sourceFolder, consistency); ok {
			containerMountFolder = remapped
			additionalMounts = append(additionalMounts, commonDirMount)
		}
	}

	containerFolder := containerMountFolder
	if sourceFolder != workspace.RootFolderPath {
		if rel, err := filepath.Rel(sourceFolder, workspace.RootFolderPath); err == nil {
			containerFolder = path.Join(containerMountFolder, filepath.ToSlash(rel))
		}
	}

	wc := &WorkspaceConfig{
		WorkspaceFolder:  containerFolder,
		WorkspaceMount:   bindMount(sourceFolder, containerMountFolder, consistency),
		AdditionalMounts: additionalMounts,
	}

	if config.WorkspaceFolder != "" {
		wc.WorkspaceFolder = config.WorkspaceFolder
	}
	if hasWorkspaceMount {
		wc.WorkspaceMount = config.WorkspaceMount
	}
	return wc
}

// bindMount formats a `type=bind` mount string, quoting the source/target when it
// contains a comma (which Docker would otherwise read as an option boundary),
// matching the TS srcQuote/tgtQuote guard.
func bindMount(source, target, consistency string) string {
	srcQuote, tgtQuote := "", ""
	if strings.Contains(source, ",") {
		srcQuote = `"`
	}
	if strings.Contains(target, ",") {
		tgtQuote = `"`
	}
	return fmt.Sprintf("type=bind,%ssource=%s%s,%starget=%s%s%s",
		srcQuote, source, srcQuote, tgtQuote, target, tgtQuote, consistency)
}

func gitWorktreeCommonDirMount(hostMountFolder, consistency string) (containerMountFolder, additionalMount string, ok bool) {
	info, err := os.Stat(filepath.Join(hostMountFolder, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	data, err := os.ReadFile(filepath.Join(hostMountFolder, ".git"))
	if err != nil {
		return "", "", false
	}
	gitdir, ok := parseGitdir(string(data))
	if !ok || filepath.IsAbs(gitdir) {
		return "", "", false
	}

	// gitdir points at .git/worktrees/<name>; the common dir is .git, two levels up.
	gitCommonDir := filepath.Clean(filepath.Join(hostMountFolder, gitdir, "..", ".."))

	sep := string(os.PathSeparator)
	var segments []string
	for current := hostMountFolder; !strings.HasPrefix(gitCommonDir, current+sep) && current != filepath.Dir(current); current = filepath.Dir(current) {
		segments = append([]string{filepath.Base(current)}, segments...)
	}
	containerMountFolder = path.Join(append([]string{"/workspaces"}, segments...)...)

	containerGitCommonDir := path.Clean(path.Join(containerMountFolder, filepath.ToSlash(gitdir), "..", ".."))
	return containerMountFolder, bindMount(gitCommonDir, containerGitCommonDir, consistency), true
}

func parseGitdir(content string) (string, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, found := strings.CutPrefix(line, "gitdir:"); found {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

func detectGitRoot(dir string) string {
	return detectGitRootUntil(dir, "")
}

func detectGitRootUntil(dir, stopAt string) string {
	dir = filepath.Clean(dir)
	if stopAt != "" {
		stopAt = filepath.Clean(stopAt)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		if dir == stopAt {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func EnvHostSubContext() HostSubContext {
	return HostSubContext{Platform: currentPlatform(), Env: EnvMap(os.Environ())}
}

func currentPlatform() string {
	if IsWindows() {
		return "win32"
	}
	return os.Getenv("GOOS_OVERRIDE")
}

func EnvMap(entries []string) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

func remarshal(raw map[string]any, target *DevContainer) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return jsonc.Unmarshal(data, target)
}
