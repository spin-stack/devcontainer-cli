package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"path"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"
)

type SubstitutionPhase uint8

const (
	PhaseHost SubstitutionPhase = iota
	PhaseIdentity
	PhaseContainer
)

type SubstitutionContext struct {
	HostSubContext
	IDLabels       map[string]string
	DevContainerID string
	ContainerEnv   map[string]string
}

// VariableResolver applies devcontainer substitutions one explicit phase at a
// time. ${...} tags are scanned by substituteTags.
type VariableResolver struct{}

func NewVariableResolver() *VariableResolver { return &VariableResolver{} }

func (r *VariableResolver) Resolve(ctx SubstitutionContext, phase SubstitutionPhase, value any) (any, error) {
	// TS substitute resolves the container workspace folder before the value
	// that may name it. Only the host phase reads it (resolveHostTag).
	if phase == PhaseHost {
		if ctx.ContainerWorkspaceFolder != "" {
			resolved, err := r.resolveString(ctx, phase, ctx.ContainerWorkspaceFolder)
			if err != nil {
				return nil, err
			}
			ctx.ContainerWorkspaceFolder = resolved
		}
	}
	return r.resolveRecursive(ctx, phase, value)
}

func (r *VariableResolver) AfterContainer(ctx SubstitutionContext, value any) (any, error) {
	return r.Resolve(ctx, PhaseContainer, value)
}

func (r *VariableResolver) BeforeContainerInto(ctx SubstitutionContext, target any) error {
	if err := r.ResolveInto(ctx, PhaseHost, target); err != nil {
		return err
	}
	return r.ResolveInto(ctx, PhaseIdentity, target)
}

// ResolveInto applies a phase to any JSON-serializable configuration value and
// unmarshals the result back into target. target must be a non-nil pointer.
func (r *VariableResolver) ResolveInto(ctx SubstitutionContext, phase SubstitutionPhase, target any) error {
	data, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("marshal substitution target: %w", err)
	}
	var value any
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

func (r *VariableResolver) resolveRecursive(ctx SubstitutionContext, phase SubstitutionPhase, value any) (any, error) {
	switch v := value.(type) {
	case string:
		return r.resolveString(ctx, phase, v)
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			resolved, err := r.resolveRecursive(ctx, phase, item)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(v))
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
			return posixBasename(ctx.LocalWorkspaceFolder), true, nil
		}
	case "containerWorkspaceFolder":
		if ctx.ContainerWorkspaceFolder != "" {
			return ctx.ContainerWorkspaceFolder, true, nil
		}
	case "containerWorkspaceFolderBasename":
		if ctx.ContainerWorkspaceFolder != "" {
			return posixBasename(ctx.ContainerWorkspaceFolder), true, nil
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
		return "", false, fmt.Errorf("'${%s}'%s can not be resolved because no environment variable name is given.", tag, location)
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

// ComputeDevContainerID produces a deterministic ID from id-labels, as TS
// devcontainerIdForLabels does: the labels' JSON.stringify with sorted keys,
// SHA-256, then the hash as a base-32 number padded to 52 digits.
func ComputeDevContainerID(idLabels map[string]string) string {
	hash := sha256.Sum256([]byte(jsonStringifySorted(idLabels)))
	str := new(big.Int).SetBytes(hash[:]).Text(32)
	// A 256-bit hash has at most 52 base-32 digits.
	return strings.Repeat("0", 52-len(str)) + str
}

// jsonStringifySorted is JSON.stringify(m, Object.keys(m).sort()): keys in
// UTF-16 code unit order (JS's sort), strings escaped as JSON.stringify does
// (encoding/json would also escape &, <, >, U+2028 and U+2029).
func jsonStringifySorted(m map[string]string) string {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b string) int {
		return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
	})
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSString(&b, k)
		b.WriteByte(':')
		writeJSString(&b, m[k])
	}
	b.WriteByte('}')
	return b.String()
}

func writeJSString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func normalizeEnvKeys(env map[string]string) map[string]string {
	m := make(map[string]string, len(env))
	for k, v := range env {
		m[strings.ToLower(k)] = v
	}
	return m
}

// posixBasename is node's path.posix.basename: trailing slashes are ignored,
// and "/" has none.
func posixBasename(p string) string {
	p = strings.TrimRight(p, "/")
	return p[strings.LastIndexByte(p, '/')+1:]
}

// winBasename is node's path.win32.basename: both / and \ separate, a drive
// letter is no name, trailing separators are ignored.
func winBasename(p string) string {
	if len(p) >= 2 && p[1] == ':' && ('a' <= p[0]|0x20 && p[0]|0x20 <= 'z') {
		p = p[2:]
	}
	p = strings.TrimRight(p, `/\`)
	return p[strings.LastIndexAny(p, `/\`)+1:]
}

// IsWindows returns true if the current platform is Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}
