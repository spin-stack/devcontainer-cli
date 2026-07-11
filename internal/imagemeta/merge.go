package imagemeta

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
		// Lifecycle hooks — always append
		if e.OnCreateCommand != nil {
			m.OnCreateCommands = append(m.OnCreateCommands, e.OnCreateCommand)
		}
		if e.UpdateContentCommand != nil {
			m.UpdateContentCommands = append(m.UpdateContentCommands, e.UpdateContentCommand)
		}
		if e.PostCreateCommand != nil {
			m.PostCreateCommands = append(m.PostCreateCommands, e.PostCreateCommand)
		}
		if e.PostStartCommand != nil {
			m.PostStartCommands = append(m.PostStartCommands, e.PostStartCommand)
		}
		if e.PostAttachCommand != nil {
			m.PostAttachCommands = append(m.PostAttachCommands, e.PostAttachCommand)
		}

		// Entrypoints — collect from features
		if e.Entrypoint != "" {
			m.Entrypoints = appendUnique(m.Entrypoints, []string{e.Entrypoint})
		}

		// Arrays — union
		m.CapAdd = appendUnique(m.CapAdd, e.CapAdd)
		m.SecurityOpt = appendUnique(m.SecurityOpt, e.SecurityOpt)
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
		if m.HostRequirements == nil && e.HostRequirements != nil {
			m.HostRequirements = e.HostRequirements
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

	return m
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
