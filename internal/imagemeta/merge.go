package imagemeta

import (
	"cmp"
	"encoding/json"
	"maps"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

type MergedConfig struct {
	ConfigFilePath        any                 `json:"configFilePath,omitempty"`
	Image                 string              `json:"image,omitempty"`
	RemoteUser            string              `json:"remoteUser,omitempty"`
	ContainerUser         string              `json:"containerUser,omitempty"`
	UserEnvProbe          string              `json:"userEnvProbe,omitempty"`
	OverrideCommand       *bool               `json:"overrideCommand,omitempty"`
	Init                  *bool               `json:"init"`
	Privileged            *bool               `json:"privileged"`
	WaitFor               string              `json:"waitFor,omitempty"`
	ShutdownAction        string              `json:"shutdownAction,omitempty"`
	HostRequirements      any                 `json:"hostRequirements,omitempty"`
	UpdateRemoteUserUID   *bool               `json:"updateRemoteUserUID,omitempty"`
	CapAdd                []string            `json:"capAdd,omitempty"`
	SecurityOpt           []string            `json:"securityOpt,omitempty"`
	PortsAttributes       map[string]any      `json:"portsAttributes"`
	OtherPortsAttributes  any                 `json:"otherPortsAttributes,omitempty"`
	ForwardPorts          []any               `json:"forwardPorts,omitempty"`
	ContainerEnv          map[string]string   `json:"containerEnv"`
	RemoteEnv             map[string]*string  `json:"remoteEnv"`
	Customizations        map[string][]any    `json:"customizations,omitempty"`
	Mounts                []any               `json:"mounts,omitempty"`
	Entrypoints           []string            `json:"entrypoints,omitempty"`
	OnCreateCommands      []any               `json:"onCreateCommands"`
	CommandOrigins        map[string][]string `json:"-"`
	UpdateContentCommands []any               `json:"updateContentCommands"`
	PostCreateCommands    []any               `json:"postCreateCommands"`
	PostStartCommands     []any               `json:"postStartCommands"`
	PostAttachCommands    []any               `json:"postAttachCommands"`
}

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

	for _, e := range entries {
		origin := cmp.Or(e.ID, "devcontainer.json")
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

		if e.Entrypoint != "" {
			m.Entrypoints = append(m.Entrypoints, e.Entrypoint)
		}

		m.CapAdd = appendUnique(m.CapAdd, e.CapAdd)
		m.SecurityOpt = appendUnique(m.SecurityOpt, e.SecurityOpt)
		m.ForwardPorts = append(m.ForwardPorts, e.ForwardPorts...)
		m.Mounts = append(m.Mounts, e.Mounts...)

		maps.Copy(m.ContainerEnv, e.ContainerEnv)
		maps.Copy(m.RemoteEnv, e.RemoteEnv)
		for k, v := range e.Customizations {
			m.Customizations[k] = append(m.Customizations[k], v)
		}
		maps.Copy(m.PortsAttributes, e.PortsAttributes)
	}

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
		if m.OverrideCommand == nil && e.OverrideCommand != nil {
			m.OverrideCommand = e.OverrideCommand
		}
		if m.UpdateRemoteUserUID == nil && e.UpdateRemoteUserUID != nil {
			m.UpdateRemoteUserUID = e.UpdateRemoteUserUID
		}
	}

	m.Mounts = dedupeMountsByTarget(m.Mounts)
	m.ForwardPorts = dedupeForwardPorts(m.ForwardPorts)
	m.HostRequirements = mergeHostRequirements(entries)

	return m
}

func isNonEmptyCommand(cmd any) bool {
	return cmd != nil && cmd != ""
}

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
	lastIdx := make(map[string]int, len(mounts))
	for i, mnt := range mounts {
		lastIdx[mountTarget(mnt)] = i
	}
	var out []any
	for i, mnt := range mounts {
		if lastIdx[mountTarget(mnt)] == i {
			out = append(out, mnt)
		}
	}
	return out
}

func dedupeForwardPorts(ports []any) []any {
	seen := make(map[any]bool, len(ports))
	var out []any
	for _, p := range ports {
		port := normalizeForwardPort(p)
		switch port.(type) {
		case string, bool, nil:
			if seen[port] {
				continue
			}
			seen[port] = true
		}
		out = append(out, denormalizePort(port))
	}
	return out
}

func normalizeForwardPort(p any) any {
	switch v := p.(type) {
	case float64:
		if v == 0 {
			return "localhost:0" // `${-0}` is "0"
		}
		return "localhost:" + jsNumberString(v)
	}
	return p
}

var localhostPort = regexp.MustCompile(`localhost:[0-9]`)

// denormalizePort is TS `/localhost:\d+/.test(port) ? parseInt(port.substring('localhost:'.length)) : port`
// for a normalized port: the substring counts UTF-16 code units from the start
// of the string, wherever the match is.
func denormalizePort(port any) any {
	s, ok := port.(string)
	if !ok || !localhostPort.MatchString(s) {
		return port
	}
	units := utf16.Encode([]rune(s))
	n, ok := jsParseInt(string(utf16.Decode(units[len("localhost:"):])))
	if !ok {
		return nil // NaN, which JSON.stringify writes as null
	}
	return n
}

const jsSpaceChars = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

func jsParseInt(s string) (float64, bool) {
	s = strings.TrimLeft(s, jsSpaceChars)
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	base, digits := 10, "0123456789"
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, digits = 16, "0123456789abcdefABCDEF"
		s = s[2:]
	}
	end := 0
	for end < len(s) && strings.IndexByte(digits, s[end]) >= 0 {
		end++
	}
	if end == 0 {
		return 0, false // mutate-exempt: the value is unused when ok is false
	}
	n, _ := new(big.Int).SetString(s[:end], base)
	f, _ := new(big.Float).SetInt(n).Float64()
	if neg && f != 0 {
		f = -f
	}
	return f, true
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

var byteUnits = []struct {
	suffix string
	size   float64
}{{"tb", 1 << 40}, {"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}}

func parseBytes(s string) float64 {
	unit := 1.0
	for _, u := range byteUnits {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			s, unit = rest, u.size
			break
		}
	}
	if !allDigits(s) {
		return 0
	}
	// Digits always parse; a count past the float64 range is +Inf with
	// ErrRange, which is what TS parseInt makes of it too.
	n, _ := strconv.ParseFloat(s, 64) // mutate-exempt: every bitSize but 32 is 64
	return n * unit
}

func jsNumberString(n float64) string {
	if n > math.MaxFloat64 {
		return "Infinity"
	}
	// encoding/json writes a finite float64 as JS does: decimal digits below
	// 1e21, exponent notation from there on; only Inf and NaN fail.
	data, _ := json.Marshal(n)
	return string(data)
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

func mergeHostRequirements(entries []Entry) any {
	if len(entries) == 0 {
		// TS takes Math.max() of no values, -Infinity, which is truthy: cpus
		// serializes as null and memory/storage as the string "-Infinity".
		return map[string]any{"cpus": nil, "memory": "-Infinity", "storage": "-Infinity"}
	}
	var cpus float64
	var memory, storage float64
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
		res["memory"] = jsNumberString(memory)
	}
	if storage != 0 {
		res["storage"] = jsNumberString(storage)
	}
	if gpu != nil {
		res["gpu"] = gpu
	}
	return res
}

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
		res["memory"] = jsNumberString(mem)
	}
	return res
}

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

func nonEmptyEntries(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if data, err := json.Marshal(e); err != nil || string(data) != "{}" {
			out = append(out, e)
		}
	}
	return out
}
