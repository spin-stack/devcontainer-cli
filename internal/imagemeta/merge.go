package imagemeta

import (
	"fmt"
	"strconv"
	"strings"
)

// MergedConfig holds the result of merging devcontainer.json config with
// image metadata entries. Lifecycle hooks are accumulated as arrays.
type MergedConfig struct {
	ConfigFilePath interface{} `json:"configFilePath,omitempty"`
	// Image is part of the mergedConfiguration output (spread from the config in
	// TS), but NOT part of the devcontainer.metadata label entry — it is set by the
	// caller from the config, not derived from metadata entries.
	Image                 string                   `json:"image,omitempty"`
	RemoteUser            string                   `json:"remoteUser,omitempty"`
	ContainerUser         string                   `json:"containerUser,omitempty"`
	UserEnvProbe          string                   `json:"userEnvProbe,omitempty"`
	OverrideCommand       *bool                    `json:"overrideCommand,omitempty"`
	Init                  *bool                    `json:"init"`
	Privileged            *bool                    `json:"privileged"`
	WaitFor               string                   `json:"waitFor,omitempty"`
	ShutdownAction        string                   `json:"shutdownAction,omitempty"`
	HostRequirements      interface{}              `json:"hostRequirements,omitempty"`
	UpdateRemoteUserUID   *bool                    `json:"updateRemoteUserUID,omitempty"`
	CapAdd                []string                 `json:"capAdd,omitempty"`
	SecurityOpt           []string                 `json:"securityOpt,omitempty"`
	PortsAttributes       map[string]interface{}   `json:"portsAttributes"`
	OtherPortsAttributes  interface{}              `json:"otherPortsAttributes,omitempty"`
	ForwardPorts          []interface{}            `json:"forwardPorts,omitempty"`
	RunArgs               []string                 `json:"runArgs,omitempty"`
	ContainerEnv          map[string]string        `json:"containerEnv"`
	RemoteEnv             map[string]*string       `json:"remoteEnv"`
	Customizations        map[string][]interface{} `json:"customizations,omitempty"`
	Mounts                []interface{}            `json:"mounts,omitempty"`
	Entrypoints           []string                 `json:"entrypoints,omitempty"`
	OnCreateCommands      []interface{}            `json:"onCreateCommands"`
	UpdateContentCommands []interface{}            `json:"updateContentCommands"`
	PostCreateCommands    []interface{}            `json:"postCreateCommands"`
	PostStartCommands     []interface{}            `json:"postStartCommands"`
	PostAttachCommands    []interface{}            `json:"postAttachCommands"`
}

// MergeConfiguration merges image metadata entries with the devcontainer config.
// Entries are processed in order; the last entry has highest priority for scalars.
// This matches the TS mergeConfiguration() in imageMetadata.ts.
func MergeConfiguration(entries []Entry) *MergedConfig {
	m := &MergedConfig{
		ContainerEnv:          make(map[string]string),
		RemoteEnv:             make(map[string]*string),
		PortsAttributes:       make(map[string]interface{}),
		Customizations:        make(map[string][]interface{}),
		OnCreateCommands:      []interface{}{},
		UpdateContentCommands: []interface{}{},
		PostCreateCommands:    []interface{}{},
		PostStartCommands:     []interface{}{},
		PostAttachCommands:    []interface{}{},
	}

	// Process entries in order. For lifecycle hooks, iterate all.
	// For scalars, the last non-empty value wins (iterate in reverse for scalars).
	for _, e := range entries {
		// Lifecycle hooks — collect every truthy command (TS `if (command)`), so an
		// empty-string command is skipped rather than emitted as a spurious "" entry.
		if isNonEmptyCommand(e.OnCreateCommand) {
			m.OnCreateCommands = append(m.OnCreateCommands, e.OnCreateCommand)
		}
		if isNonEmptyCommand(e.UpdateContentCommand) {
			m.UpdateContentCommands = append(m.UpdateContentCommands, e.UpdateContentCommand)
		}
		if isNonEmptyCommand(e.PostCreateCommand) {
			m.PostCreateCommands = append(m.PostCreateCommands, e.PostCreateCommand)
		}
		if isNonEmptyCommand(e.PostStartCommand) {
			m.PostStartCommands = append(m.PostStartCommands, e.PostStartCommand)
		}
		if isNonEmptyCommand(e.PostAttachCommand) {
			m.PostAttachCommands = append(m.PostAttachCommands, e.PostAttachCommand)
		}

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
		for k, v := range e.ContainerEnv {
			m.ContainerEnv[k] = v
		}
		for k, v := range e.RemoteEnv {
			m.RemoteEnv[k] = v
		}
		// customizations are grouped by key into arrays (one entry per metadata
		// entry that sets that key), matching TS. This preserves every feature's
		// extensions/settings instead of last-wins-clobbering them.
		for k, v := range e.Customizations {
			m.Customizations[k] = append(m.Customizations[k], v)
		}
		// portsAttributes — later entries override matching keys (Object.assign).
		for k, v := range e.PortsAttributes {
			m.PortsAttributes[k] = v
		}
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
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
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
func isNonEmptyCommand(cmd interface{}) bool {
	return cmd != nil && cmd != ""
}

// mountTarget returns the container-side target of a mount (object or string
// spec), used as the de-dup key. A string with no target= key is its own key so
// distinct strings never collide.
func mountTarget(mnt interface{}) string {
	switch v := mnt.(type) {
	case map[string]interface{}:
		t, _ := v["target"].(string)
		return t
	case string:
		for _, part := range strings.Split(v, ",") {
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

func dedupeMountsByTarget(mounts []interface{}) []interface{} {
	if len(mounts) == 0 {
		return mounts
	}
	lastIdx := make(map[string]int, len(mounts))
	for i, mnt := range mounts {
		lastIdx[mountTarget(mnt)] = i
	}
	out := make([]interface{}, 0, len(mounts))
	for i, mnt := range mounts {
		if lastIdx[mountTarget(mnt)] == i {
			out = append(out, mnt)
		}
	}
	return out
}

// forwardPortNumber returns (N, true) for a numeric port (number or numeric
// string is NOT treated as a number by TS — only actual JSON numbers are).
func forwardPortNumber(p interface{}) (int, bool) {
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

func dedupeForwardPorts(ports []interface{}) []interface{} {
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
	out := make([]interface{}, 0, len(keys))
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

func hrFloat(v interface{}) float64 {
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

func hrString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "0"
}

// mergeHostRequirements takes the per-field max of cpus/memory/storage and merges
// gpu across all entries, matching TS mergeHostRequirements. Returns nil when no
// entry sets any requirement.
func mergeHostRequirements(entries []Entry) interface{} {
	var cpus float64
	var memory, storage int64
	var gpu interface{}
	for _, e := range entries {
		hr, ok := e.HostRequirements.(map[string]interface{})
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
	res := map[string]interface{}{"cpus": cpus}
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
func mergeGPURequirements(a, b interface{}) interface{} {
	if a == nil || a == false {
		return b
	}
	if b == nil || b == false {
		return a
	}
	if a == "optional" && b == "optional" {
		return "optional"
	}
	ao, _ := a.(map[string]interface{})
	bo, _ := b.(map[string]interface{})
	cores := hrFloat(ao["cores"])
	if c := hrFloat(bo["cores"]); c > cores {
		cores = c
	}
	mem := parseBytes(hrString(ao["memory"]))
	if m := parseBytes(hrString(bo["memory"])); m > mem {
		mem = m
	}
	res := map[string]interface{}{}
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
