package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"path"
	"runtime"
	"sort"
	"strings"
)

type SubstitutionPhase uint8

const (
	PhaseHost SubstitutionPhase = iota
	PhaseIdentity
	PhaseContainer
	PhaseTemplate
)

type SubstitutionContext struct {
	HostSubContext
	IDLabels        map[string]string
	DevContainerID  string
	ContainerEnv    map[string]string
	TemplateOptions map[string]string
}

// VariableResolver applies devcontainer substitutions one explicit phase at a
// time. ${...} tags are scanned by substituteTags.
type VariableResolver struct{}

func NewVariableResolver() *VariableResolver { return &VariableResolver{} }

func (r *VariableResolver) Resolve(ctx SubstitutionContext, phase SubstitutionPhase, value interface{}) (interface{}, error) {
	if phase == PhaseHost && ctx.ContainerWorkspaceFolder != "" {
		resolved, err := r.resolveString(ctx, phase, ctx.ContainerWorkspaceFolder)
		if err != nil {
			return nil, err
		}
		ctx.ContainerWorkspaceFolder = resolved
	}
	return r.resolveRecursive(ctx, phase, value)
}

// BeforeContainer always applies both pre-container phases in canonical order.
func (r *VariableResolver) BeforeContainer(ctx SubstitutionContext, value interface{}) (interface{}, error) {
	value, err := r.Resolve(ctx, PhaseHost, value)
	if err != nil {
		return nil, err
	}
	return r.Resolve(ctx, PhaseIdentity, value)
}

func (r *VariableResolver) AfterContainer(ctx SubstitutionContext, value interface{}) (interface{}, error) {
	return r.Resolve(ctx, PhaseContainer, value)
}

func (r *VariableResolver) BeforeContainerInto(ctx SubstitutionContext, target interface{}) error {
	if err := r.ResolveInto(ctx, PhaseHost, target); err != nil {
		return err
	}
	return r.ResolveInto(ctx, PhaseIdentity, target)
}

func (r *VariableResolver) AfterContainerInto(ctx SubstitutionContext, target interface{}) error {
	return r.ResolveInto(ctx, PhaseContainer, target)
}

// ResolveInto applies a phase to any JSON-serializable configuration value and
// unmarshals the result back into target. target must be a non-nil pointer.
func (r *VariableResolver) ResolveInto(ctx SubstitutionContext, phase SubstitutionPhase, target interface{}) error {
	data, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("marshal substitution target: %w", err)
	}
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode substitution target: %w", err)
	}
	resolved, err := r.Resolve(ctx, phase, value)
	if err != nil {
		return err
	}
	data, err = json.Marshal(resolved)
	if err != nil {
		return fmt.Errorf("marshal substituted value: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode substituted value: %w", err)
	}
	return nil
}

func (r *VariableResolver) resolveRecursive(ctx SubstitutionContext, phase SubstitutionPhase, value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return r.resolveString(ctx, phase, v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			resolved, err := r.resolveRecursive(ctx, phase, item)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, item := range v {
			resolved, err := r.resolveRecursive(ctx, phase, item)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

func (r *VariableResolver) resolveString(ctx SubstitutionContext, phase SubstitutionPhase, value string) (string, error) {
	return substituteTags(value, func(tag string) (string, bool, error) {
		return r.resolveTag(ctx, phase, tag)
	})
}

// substituteTags rewrites every ${...} tag in s using replace. replace returns
// the replacement text and whether it handled the tag; unhandled tags are left
// verbatim so a later phase can resolve them. An unterminated "${" is kept as-is.
func substituteTags(s string, replace func(tag string) (string, bool, error)) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i+2:]
		j := strings.IndexByte(s, '}')
		if j < 0 {
			b.WriteString("${")
			b.WriteString(s)
			break
		}
		tag := s[:j]
		replacement, resolved, err := replace(tag)
		if err != nil {
			return "", err
		}
		if !resolved {
			replacement = "${" + tag + "}"
		}
		b.WriteString(replacement)
		s = s[j+1:]
	}
	return b.String(), nil
}

func (r *VariableResolver) resolveTag(ctx SubstitutionContext, phase SubstitutionPhase, tag string) (string, bool, error) {
	parts := strings.Split(tag, ":")
	variable := parts[0]
	args := parts[1:]
	switch phase {
	case PhaseHost:
		return resolveHostTag(ctx.HostSubContext, variable, args, tag)
	case PhaseIdentity:
		if variable != "devcontainerId" {
			return "", false, nil
		}
		id := ctx.DevContainerID
		if id == "" && ctx.IDLabels != nil {
			id = ComputeDevContainerID(ctx.IDLabels)
		}
		return id, id != "", nil
	case PhaseContainer:
		if variable != "containerEnv" {
			return "", false, nil
		}
		return resolveEnvTag(ctx.Platform == "win32", ctx.ContainerEnv, args, tag, ctx.ConfigFilePath)
	case PhaseTemplate:
		if variable != "templateOption" || len(args) != 1 {
			return "", false, nil
		}
		value, ok := ctx.TemplateOptions[strings.TrimSpace(args[0])]
		return value, ok, nil
	default:
		return "", false, fmt.Errorf("unknown substitution phase %d", phase)
	}
}

func resolveHostTag(ctx HostSubContext, variable string, args []string, tag string) (string, bool, error) {
	isWin := ctx.Platform == "win32"
	switch variable {
	case "env", "localEnv":
		return resolveEnvTag(isWin, ctx.Env, args, tag, ctx.ConfigFilePath)
	case "localWorkspaceFolder":
		if ctx.LocalWorkspaceFolder != "" {
			return ctx.LocalWorkspaceFolder, true, nil
		}
	case "localWorkspaceFolderBasename":
		if ctx.LocalWorkspaceFolder != "" {
			if isWin {
				return winBasename(ctx.LocalWorkspaceFolder), true, nil
			}
			return path.Base(ctx.LocalWorkspaceFolder), true, nil
		}
	case "containerWorkspaceFolder":
		if ctx.ContainerWorkspaceFolder != "" {
			return ctx.ContainerWorkspaceFolder, true, nil
		}
	case "containerWorkspaceFolderBasename":
		if ctx.ContainerWorkspaceFolder != "" {
			return path.Base(ctx.ContainerWorkspaceFolder), true, nil
		}
	}
	return "", false, nil
}

func resolveEnvTag(isWin bool, env map[string]string, args []string, tag, configFilePath string) (string, bool, error) {
	if len(args) == 0 {
		location := ""
		if configFilePath != "" {
			location = " in " + path.Base(configFilePath)
		}
		return "", false, fmt.Errorf("'${%s}'%s cannot be resolved because no environment variable name is given", tag, location)
	}
	name := args[0]
	if isWin {
		name = strings.ToLower(name)
		env = normalizeEnvKeys(env)
	}
	if value, ok := env[name]; ok {
		return value, true, nil
	}
	if len(args) > 1 {
		// Rejoin the remaining parts so a default value may itself contain colons
		// (e.g. ${localEnv:REG:my.registry.io:5000/img}); only the first colon
		// separates the variable name from its default.
		return strings.Join(args[1:], ":"), true, nil
	}
	return "", true, nil
}

// HostSubContext holds values available before a container starts.
type HostSubContext struct {
	Platform                 string // "linux", "darwin", "win32"
	LocalWorkspaceFolder     string
	ContainerWorkspaceFolder string
	Env                      map[string]string
	ConfigFilePath           string // for error messages
}

// ComputeDevContainerID produces a deterministic ID from id-labels,
// matching the TS implementation exactly: SHA256 → BigInt → base32 padded to 52.
func ComputeDevContainerID(idLabels map[string]string) string {
	// Sort keys for determinism (matches JSON.stringify with sorted keys)
	keys := make([]string, 0, len(idLabels))
	for k := range idLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	data, _ := json.Marshal(sortedMap(keys, idLabels))
	hash := sha256.Sum256(data)

	// Convert hash to BigInt, then to base32 string, padded to 52 chars
	n := new(big.Int).SetBytes(hash[:])
	str := n.Text(32)
	if len(str) < 52 {
		str = strings.Repeat("0", 52-len(str)) + str
	}
	return str
}

func normalizeEnvKeys(env map[string]string) map[string]string {
	m := make(map[string]string, len(env))
	for k, v := range env {
		m[strings.ToLower(k)] = v
	}
	return m
}

func winBasename(p string) string {
	// Handle both / and \ separators on Windows
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func sortedMap(keys []string, m map[string]string) map[string]string {
	// json.Marshal on map[string]string sorts keys by default in Go
	result := make(map[string]string, len(keys))
	for _, k := range keys {
		result[k] = m[k]
	}
	return result
}

// IsWindows returns true if the current platform is Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}
