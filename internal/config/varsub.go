package config

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var variableRegexp = regexp.MustCompile(`\$\{(.*?)\}`)

// HostSubContext holds values available before a container starts.
type HostSubContext struct {
	Platform                 string // "linux", "darwin", "win32"
	LocalWorkspaceFolder     string
	ContainerWorkspaceFolder string
	Env                      map[string]string
	ConfigFilePath           string // for error messages
}

// SubstituteHost resolves ${localEnv:X}, ${localWorkspaceFolder}, etc.
func SubstituteHost(ctx HostSubContext, value interface{}) interface{} {
	replace := hostReplacer(ctx)
	return substituteRecursive(replace, value)
}

// SubstituteHostString is the typed string variant of SubstituteHost.
func SubstituteHostString(ctx HostSubContext, value string) string {
	return resolveString(hostReplacer(ctx), value)
}

func hostReplacer(ctx HostSubContext) replaceFn {
	env := ctx.Env
	if ctx.Platform == "win32" {
		env = normalizeEnvKeys(env)
	}
	isWin := ctx.Platform == "win32"

	// Resolve containerWorkspaceFolder first if it contains variables
	if ctx.ContainerWorkspaceFolder != "" {
		ctx.ContainerWorkspaceFolder = resolveString(func(match, variable string, args []string) string {
			return replaceHostVar(isWin, env, ctx, match, variable, args)
		}, ctx.ContainerWorkspaceFolder)
	}

	return func(match, variable string, args []string) string {
		return replaceHostVar(isWin, env, ctx, match, variable, args)
	}
}

// SubstituteDevContainerID resolves ${devcontainerId} from id-labels.
func SubstituteDevContainerID(idLabels map[string]string, value interface{}) interface{} {
	var cachedID string
	return substituteRecursive(func(match, variable string, _ []string) string {
		if variable == "devcontainerId" {
			if cachedID == "" && idLabels != nil {
				cachedID = ComputeDevContainerID(idLabels)
			}
			return substituteDevContainerID(match, cachedID)
		}
		return match
	}, value)
}

// SubstituteDevContainerIDString resolves ${devcontainerId} when the ID has
// already been computed. Keeping the parsing here prevents callers from
// implementing variable substitution with ad-hoc string replacements.
func SubstituteDevContainerIDString(devcontainerID, value string) string {
	return resolveString(func(match, variable string, _ []string) string {
		if variable == "devcontainerId" {
			return substituteDevContainerID(match, devcontainerID)
		}
		return match
	}, value)
}

// SubstituteTemplateOptions resolves ${templateOption:name} using the supplied
// option values. Unknown options remain unchanged.
func SubstituteTemplateOptions(options map[string]string, value string) string {
	return resolveString(func(match, variable string, args []string) string {
		if variable != "templateOption" || len(args) == 0 {
			return match
		}
		if replacement, ok := options[strings.TrimSpace(args[0])]; ok {
			return replacement
		}
		return match
	}, value)
}

// SubstituteContainer resolves ${containerEnv:X} after a container is running.
func SubstituteContainer(platform string, containerEnv map[string]string, value interface{}) interface{} {
	isWin := platform == "win32"
	env := containerEnv
	if isWin {
		env = normalizeEnvKeys(env)
	}
	return substituteRecursive(func(match, variable string, args []string) string {
		if variable == "containerEnv" {
			return lookupEnvValue(isWin, env, args, match)
		}
		return match
	}, value)
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

// --- Internal ---

type replaceFn func(match, variable string, args []string) string

func substituteDevContainerID(match, devcontainerID string) string {
	if devcontainerID == "" {
		return match
	}
	return devcontainerID
}

func substituteRecursive(replace replaceFn, value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return resolveString(replace, v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = substituteRecursive(replace, item)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for k, val := range v {
			result[k] = substituteRecursive(replace, val)
		}
		return result
	default:
		return value
	}
}

func resolveString(replace replaceFn, value string) string {
	return variableRegexp.ReplaceAllStringFunc(value, func(match string) string {
		inner := match[2 : len(match)-1] // strip ${ and }
		parts := strings.SplitN(inner, ":", 2)
		variable := parts[0]
		var args []string
		if len(parts) > 1 {
			args = parts[1:]
		}
		return replace(match, variable, args)
	})
}

func replaceHostVar(isWin bool, env map[string]string, ctx HostSubContext, match, variable string, args []string) string {
	switch variable {
	case "env", "localEnv":
		return lookupEnvValue(isWin, env, args, match)
	case "localWorkspaceFolder":
		if ctx.LocalWorkspaceFolder != "" {
			return ctx.LocalWorkspaceFolder
		}
		return match
	case "localWorkspaceFolderBasename":
		if ctx.LocalWorkspaceFolder != "" {
			if isWin {
				return winBasename(ctx.LocalWorkspaceFolder)
			}
			return path.Base(ctx.LocalWorkspaceFolder)
		}
		return match
	case "containerWorkspaceFolder":
		if ctx.ContainerWorkspaceFolder != "" {
			return ctx.ContainerWorkspaceFolder
		}
		return match
	case "containerWorkspaceFolderBasename":
		if ctx.ContainerWorkspaceFolder != "" {
			return path.Base(ctx.ContainerWorkspaceFolder)
		}
		return match
	default:
		return match
	}
}

func lookupEnvValue(isWin bool, env map[string]string, args []string, match string) string {
	if len(args) == 0 {
		return match // no variable name given
	}
	// args[0] may contain additional :-separated parts for default value
	argParts := strings.SplitN(args[0], ":", 2)
	varName := argParts[0]
	if isWin {
		varName = strings.ToLower(varName)
	}
	if val, ok := env[varName]; ok {
		return val
	}
	// Default value
	if len(argParts) > 1 {
		return argParts[1]
	}
	// Missing env → empty string (shell convention)
	return ""
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
