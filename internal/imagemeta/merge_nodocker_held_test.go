package imagemeta

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeConfigurationScalarsTakeTheLastEntryThatSetsThem(t *testing.T) {
	m := MergeConfiguration(mustEntries(t, `[
		{"remoteUser": "u0", "containerUser": "c0", "userEnvProbe": "none", "waitFor": "onCreateCommand",
		 "shutdownAction": "none", "otherPortsAttributes": {"onAutoForward": "ignore"},
		 "overrideCommand": true, "updateRemoteUserUID": true},
		{"remoteUser": "u1", "containerUser": "c1", "userEnvProbe": "loginShell", "waitFor": "postCreateCommand",
		 "shutdownAction": "stopContainer", "otherPortsAttributes": {"onAutoForward": "silent"},
		 "overrideCommand": false, "updateRemoteUserUID": false},
		{"containerEnv": {"A": "1"}}
	]`))
	got := map[string]any{
		"remoteUser":           m.RemoteUser,
		"containerUser":        m.ContainerUser,
		"userEnvProbe":         m.UserEnvProbe,
		"waitFor":              m.WaitFor,
		"shutdownAction":       m.ShutdownAction,
		"otherPortsAttributes": m.OtherPortsAttributes,
		"overrideCommand":      m.OverrideCommand,
		"updateRemoteUserUID":  m.UpdateRemoteUserUID,
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"containerUser":"c1","otherPortsAttributes":{"onAutoForward":"silent"},"overrideCommand":false,` +
		`"remoteUser":"u1","shutdownAction":"stopContainer","updateRemoteUserUID":false,` +
		`"userEnvProbe":"loginShell","waitFor":"postCreateCommand"}`
	if string(data) != want {
		t.Errorf("merged scalars = %s\nwant %s", data, want)
	}
}

func TestMergeConfigurationHostRequirementsAsTSSerializesThem(t *testing.T) {
	tests := []struct {
		name, entries, want string
	}{
		{"largest storage first, no cpus", `[{"hostRequirements": {"memory": "4gb", "storage": "32gb"}}, {"hostRequirements": {"storage": "16gb"}}]`,
			`{"cpus":0,"memory":"4294967296","storage":"34359738368"}`},
		{"cpus only", `[{"hostRequirements": {"cpus": 2}}]`, `{"cpus":2}`},
		{"gpu true alone", `[{"hostRequirements": {"gpu": true}}]`, `{"cpus":0,"gpu":true}`},
		{"gpu true then no gpu", `[{"hostRequirements": {"gpu": true}}, {"hostRequirements": {"cpus": 1}}]`, `{"cpus":1,"gpu":true}`},
		{"gpu optional twice", `[{"hostRequirements": {"gpu": "optional"}}, {"hostRequirements": {"gpu": "optional"}}]`, `{"cpus":0,"gpu":"optional"}`},
		{"gpu optional then true", `[{"hostRequirements": {"gpu": "optional"}}, {"hostRequirements": {"gpu": true}}]`, `{"cpus":0,"gpu":{}}`},
		{"gpu true then optional", `[{"hostRequirements": {"gpu": true}}, {"hostRequirements": {"gpu": "optional"}}]`, `{"cpus":0,"gpu":{}}`},
		{"gpu cores only", `[{"hostRequirements": {"gpu": {"cores": 2}}}, {"hostRequirements": {"gpu": true}}]`, `{"cpus":0,"gpu":{"cores":2}}`},
		{"gpu memory only", `[{"hostRequirements": {"gpu": {"memory": "2gb"}}}, {"hostRequirements": {"gpu": true}}]`, `{"cpus":0,"gpu":{"memory":"2147483648"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(MergeConfiguration(mustEntries(t, tt.entries)).HostRequirements)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("hostRequirements = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestMergeConfigurationLeavesMountsUnsetWithoutMounts(t *testing.T) {
	none := MergeConfiguration([]Entry{{RemoteUser: "x"}})
	if none.Mounts != nil {
		t.Errorf("mounts = %#v, want nil", none.Mounts)
	}
	if none.ForwardPorts != nil {
		t.Errorf("forwardPorts = %#v, want nil (TS mergeForwardPorts: undefined)", none.ForwardPorts)
	}
	one := []any{"type=volume,source=v,target=/data"}
	if got := MergeConfiguration([]Entry{{Mounts: one}}).Mounts; !reflect.DeepEqual(got, one) {
		t.Errorf("mounts = %#v, want %#v", got, one)
	}
}
