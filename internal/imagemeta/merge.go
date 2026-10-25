package imagemeta

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MergedConfig holds the result of merging devcontainer.json config with
// image metadata entries. Lifecycle hooks are accumulated as arrays.
type MergedConfig struct {
	ConfigFilePath any `json:"configFilePath,omitempty"`
	// Image is part of the mergedConfiguration output (spread from the config in
	// TS), but NOT part of the devcontainer.metadata label entry — it is set by the
	// caller from the config, not derived from metadata entries.
	Image                string             `json:"image,omitempty"`
	RemoteUser           string             `json:"remoteUser,omitempty"`
	ContainerUser        string             `json:"containerUser,omitempty"`
	UserEnvProbe         string             `json:"userEnvProbe,omitempty"`
	OverrideCommand      *bool              `json:"overrideCommand,omitempty"`
	Init                 *bool              `json:"init"`
	Privileged           *bool              `json:"privileged"`
	WaitFor              string             `json:"waitFor,omitempty"`
	ShutdownAction       string             `json:"shutdownAction,omitempty"`
	HostRequirements     any                `json:"hostRequirements,omitempty"`
	UpdateRemoteUserUID  *bool              `json:"updateRemoteUserUID,omitempty"`
	CapAdd               []string           `json:"capAdd,omitempty"`
	SecurityOpt          []string           `json:"securityOpt,omitempty"`
	PortsAttributes      map[string]any     `json:"portsAttributes"`
	OtherPortsAttributes any                `json:"otherPortsAttributes,omitempty"`
	ForwardPorts         []any              `json:"forwardPorts,omitempty"`
	RunArgs              []string           `json:"runArgs,omitempty"`
	ContainerEnv         map[string]string  `json:"containerEnv"`
	RemoteEnv            map[string]*string `json:"remoteEnv"`
	Customizations       map[string][]any   `json:"customizations,omitempty"`
	Mounts               []any              `json:"mounts,omitempty"`
	Entrypoints          []string           `json:"entrypoints,omitempty"`
	OnCreateCommands     []any              `json:"onCreateCommands"`
	// CommandOrigins is, per hook name, where each of its commands comes from -
	// a Feature's id or "devcontainer.json" - in the order of the commands, as
	// TS lifecycleCommandOriginMapFromMetadata records it for error messages.
	CommandOrigins        map[string][]string `json:"-"`
	UpdateContentCommands []any               `json:"updateContentCommands"`
	PostCreateCommands    []any               `json:"postCreateCommands"`
	PostStartCommands     []any               `json:"postStartCommands"`
	PostAttachCommands    []any               `json:"postAttachCommands"`
}

// MergeConfiguration merges image metadata entries with the devcontainer config.
// Entries are processed in order; the last entry has highest priority for scalars.
// This matches the TS mergeConfiguration() in imageMetadata.ts.
func MergeConfiguration(entries []Entry) *MergedConfig {
	entries = nonEmptyEntries(entries)
	m := &MergedConfig{
		ContainerEnv:          make(map[string]string),
		RemoteEnv:             make(map[string]*string),
		PortsAttributes:       make(map[string]any),
		Customizations:        make(map[string][]any),
		OnCreateCommands:      []any{},
		UpdateContentCommands: []any{},
		PostCreateCommands:    []any{},
		PostStartCommands:     []any{},
		PostAttachCommands:    []any{},
		CommandOrigins:        map[string][]string{},
	}

	// Process entries in order. For lifecycle hooks, iterate all.
	// For scalars, the last non-empty value wins (iterate in reverse for scalars).
	for _, e := range entries {
		// Lifecycle hooks — collect every truthy command (TS `if (command)`), so an
		// empty-string command is skipped rather than emitted as a spurious "" entry.
		origin := cmp.Or(e.ID, "devcontainer.json") // only Features have ids in the metadata
		collect := func(hook string, list *[]any, command any) {
			if isNonEmptyCommand(command) {
				*list = append(*list, command)
				m.CommandOrigins[hook] = append(m.CommandOrigins[hook], origin)
			}
		}
		collect("onCreateCommand", &m.OnCreateCommands, e.OnCreateCommand)
		collect("updateContentCommand", &m.UpdateContentCommands, e.UpdateContentCommand)
		collect("postCreateCommand", &m.PostCreateCommands, e.PostCreateCommand)
		collect("postStartCommand", &m.PostStartCommands, e.PostStartCommand)
		collect("postAttachCommand", &m.PostAttachCommands, e.PostAttachCommand)

		// Entrypoints — collect ALL non-empty entrypoints (TS collectOrUndefined has
		// NO de-dup): two features that register the same entrypoint script must both
		// run, so appendUnique would wrongly drop one.
		if e.Entrypoint != "" {
			m.Entrypoints = append(m.Entrypoints, e.Entrypoint)
		}

		// Arrays — union
		m.CapAdd = appendUnique(m.CapAdd, e.CapAdd)
		m.SecurityOpt = appendUnique(m.SecurityOpt, e.SecurityOpt)
		// forwardPorts/mounts accumulate here and are de-duplicated after the loop.
		m.ForwardPorts = append(m.ForwardPorts, e.ForwardPorts...)
		m.Mounts = append(m.Mounts, e.Mounts...)

		// Maps — merge (later wins)
		maps.Copy(m.ContainerEnv, e.ContainerEnv)
		maps.Copy(m.RemoteEnv, e.RemoteEnv)
		// customizations are grouped by key into arrays (one entry per metadata
		// entry that sets that key), matching TS. This preserves every feature's
		// extensions/settings instead of last-wins-clobbering them.
		for k, v := range e.Customizations {
			m.Customizations[k] = append(m.Customizations[k], v)
		}
		// portsAttributes — later entries override matching keys (Object.assign).
		maps.Copy(m.PortsAttributes, e.PortsAttributes)
	}

	// init/privileged are OR across all entries (true if any entry requests it),
	// matching TS imageMetadata.some(...). Previously last-non-nil-wins could drop
	// a feature's init:true and the container started without tini.
	initVal, privilegedVal := false, false
	for _, e := range entries {
		if e.Init != nil && *e.Init {
			initVal = true
		}
		if e.Privileged != nil && *e.Privileged {
			privilegedVal = true
		}
	}
	m.Init = &initVal
	m.Privileged = &privilegedVal

	// Scalars — last non-empty wins (iterate in reverse)
	for _, e := range slices.Backward(entries) {

		if m.RemoteUser == "" && e.RemoteUser != "" {
			m.RemoteUser = e.RemoteUser
		}
		if m.ContainerUser == "" && e.ContainerUser != "" {
			m.ContainerUser = e.ContainerUser
		}
		if m.UserEnvProbe == "" && e.UserEnvProbe != "" {
			m.UserEnvProbe = e.UserEnvProbe
		}
		if m.WaitFor == "" && e.WaitFor != "" {
			m.WaitFor = e.WaitFor
		}
		if m.ShutdownAction == "" && e.ShutdownAction != "" {
			m.ShutdownAction = e.ShutdownAction
		}
		if m.OtherPortsAttributes == nil && e.OtherPortsAttributes != nil {
			m.OtherPortsAttributes = e.OtherPortsAttributes
		}
		if len(m.RunArgs) == 0 && len(e.RunArgs) > 0 {
			m.RunArgs = append([]string{}, e.RunArgs...)
		}
		if m.OverrideCommand == nil && e.OverrideCommand != nil {
			m.OverrideCommand = e.OverrideCommand
		}
		if m.UpdateRemoteUserUID == nil && e.UpdateRemoteUserUID != nil {
			m.UpdateRemoteUserUID = e.UpdateRemoteUserUID
		}
	}

	// mounts: keep the LAST mount per target (TS mergeMounts de-dupes by target),
	// so a later feature/config mount overrides an earlier one to the same path.
	m.Mounts = dedupeMountsByTarget(m.Mounts)
	// forwardPorts: de-dupe, treating the number N and the string "localhost:N" as
	// the same port (TS mergeForwardPorts normalizes then Set-de-dupes).
	m.ForwardPorts = dedupeForwardPorts(m.ForwardPorts)
	// hostRequirements: per-field max across all entries (TS mergeHostRequirements),
	// not last-wins of the whole object.
	m.HostRequirements = mergeHostRequirements(entries)

	return m
}

// isNonEmptyCommand reports whether a lifecycle command is "truthy" the way TS
// treats it: a nil or empty-string command is skipped; arrays/objects are kept.
func isNonEmptyCommand(cmd any) bool {
	return cmd != nil && cmd != ""
}

// mountTarget returns the container-side target of a mount (object or string
// spec), used as the de-dup key. A string with no target= key is its own key so
// distinct strings never collide.
func mountTarget(mnt any) string {
	switch v := mnt.(type) {
	case map[string]any:
		t, _ := v["target"].(string)
		return t
	case string:
		for part := range strings.SplitSeq(v, ",") {
			k, val, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "target", "destination", "dst":
				return val
			}
		}
		return v
	}
	return ""
}

func dedupeMountsByTarget(mounts []any) []any {
	if len(mounts) == 0 {
		return mounts
	}
	lastIdx := make(map[string]int, len(mounts))
	for i, mnt := range mounts {
		lastIdx[mountTarget(mnt)] = i
	}
	out := make([]any, 0, len(mounts))
	for i, mnt := range mounts {
		if lastIdx[mountTarget(mnt)] == i {
			out = append(out, mnt)
		}
	}
	return out
}

// forwardPortNumber returns (N, true) for a numeric port (number or numeric
// string is NOT treated as a number by TS — only actual JSON numbers are).
func forwardPortNumber(p any) (int, bool) {
	switch v := p.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

func dedupeForwardPorts(ports []any) []any {
	if len(ports) == 0 {
		return ports
	}
	seen := make(map[string]bool, len(ports))
	var keys []string
	for _, p := range ports {
		key := ""
		if n, ok := forwardPortNumber(p); ok {
			key = fmt.Sprintf("localhost:%d", n)
		} else {
			key = fmt.Sprintf("%v", p)
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	// Denormalize: a "localhost:N" key becomes the number N; anything else stays a
	// string — matching TS's final .map(port => /localhost:\d+/.test ? parseInt : port).
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		if n, ok := localhostPort(k); ok {
			out = append(out, float64(n))
		} else {
			out = append(out, k)
		}
	}
	return out
}

// localhostPort parses a "localhost:<n>" key (all-digit port) back to its number.
func localhostPort(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "localhost:")
	if !ok || !allDigits(rest) {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseBytes mirrors the TS parseBytes: "8gb" -> 8589934592, unrecognized -> 0.
// The grammar is <digits> with an optional [tgmk]b suffix.
func parseBytes(s string) int64 {
	unit := int64(1)
	if len(s) >= 2 && s[len(s)-1] == 'b' {
		switch s[len(s)-2] {
		case 't':
			unit, s = 1<<40, s[:len(s)-2]
		case 'g':
			unit, s = 1<<30, s[:len(s)-2]
		case 'm':
			unit, s = 1<<20, s[:len(s)-2]
		case 'k':
			unit, s = 1<<10, s[:len(s)-2]
		}
	}
	if !allDigits(s) {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * unit
}

func hrFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func hrString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "0"
}

// mergeHostRequirements takes the per-field max of cpus/memory/storage and merges
// gpu across all entries, matching TS mergeHostRequirements. Returns nil when no
// entry sets any requirement.
func mergeHostRequirements(entries []Entry) any {
	if len(entries) == 0 {
		// TS takes Math.max() of no values, -Infinity, which is truthy: cpus
		// serializes as null and memory/storage as the string "-Infinity".
		return map[string]any{"cpus": nil, "memory": "-Infinity", "storage": "-Infinity"}
	}
	var cpus float64
	var memory, storage int64
	var gpu any
	for _, e := range entries {
		hr, ok := e.HostRequirements.(map[string]any)
		if !ok {
			continue
		}
		if c := hrFloat(hr["cpus"]); c > cpus {
			cpus = c
		}
		if b := parseBytes(hrString(hr["memory"])); b > memory {
			memory = b
		}
		if b := parseBytes(hrString(hr["storage"])); b > storage {
			storage = b
		}
		gpu = mergeGPURequirements(gpu, hr["gpu"])
	}
	if cpus == 0 && memory == 0 && storage == 0 && gpu == nil {
		return nil
	}
	res := map[string]any{"cpus": cpus}
	if memory != 0 {
		res["memory"] = strconv.FormatInt(memory, 10)
	}
	if storage != 0 {
		res["storage"] = strconv.FormatInt(storage, 10)
	}
	if gpu != nil {
		res["gpu"] = gpu
	}
	return res
}

// mergeGPURequirements ports TS mergeGpuRequirements: undefined/false yields the
// other; two "optional" stay "optional"; otherwise take the per-field max of the
// object forms (a non-object contributes {}).
func mergeGPURequirements(a, b any) any {
	if a == nil || a == false {
		return b
	}
	if b == nil || b == false {
		return a
	}
	if a == "optional" && b == "optional" {
		return "optional"
	}
	ao, _ := a.(map[string]any)
	bo, _ := b.(map[string]any)
	cores := hrFloat(ao["cores"])
	if c := hrFloat(bo["cores"]); c > cores {
		cores = c
	}
	mem := parseBytes(hrString(ao["memory"]))
	if m := parseBytes(hrString(bo["memory"])); m > mem {
		mem = m
	}
	res := map[string]any{}
	if cores != 0 {
		res["cores"] = cores
	}
	if mem != 0 {
		res["memory"] = strconv.FormatInt(mem, 10)
	}
	return res
}

// appendUnique appends items from src to dst, skipping duplicates.
func appendUnique(dst, src []string) []string {
	seen := make(map[string]bool, len(dst))
	for _, s := range dst {
		seen[s] = true
	}
	for _, s := range src {
		if !seen[s] {
			dst = append(dst, s)
			seen[s] = true
		}
	}
	return dst
}

// nonEmptyEntries drops the entries that set no property, as TS
// getDevcontainerMetadata does (`.filter(config => Object.keys(config).length)`):
// a config with nothing to contribute adds no entry to the merge or the label.
func nonEmptyEntries(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if data, err := json.Marshal(e); err != nil || string(data) != "{}" {
			out = append(out, e)
		}
	}
	return out
}
