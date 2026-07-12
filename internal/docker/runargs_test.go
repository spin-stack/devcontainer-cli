package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
)

func TestParseMountSpec(t *testing.T) {
	m, err := ParseMountSpec("type=bind,source=/a,target=/b,consistency=cached,readonly")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "bind" || m.Source != "/a" || m.Target != "/b" || string(m.Consistency) != "cached" || !m.ReadOnly {
		t.Errorf("mount = %+v", m)
	}
	m2, _ := ParseMountSpec("src=/x,dst=/y")
	if m2.Type != "bind" || m2.Source != "/x" || m2.Target != "/y" {
		t.Errorf("mount2 = %+v", m2)
	}
	if _, err := ParseMountSpec("type=volume,source=vol"); err == nil {
		t.Error("expected error for missing target")
	}
}

func TestParseMountSpec_BareRO(t *testing.T) {
	// "ro" as a bare flag (no =value) must set ReadOnly, and an explicit
	// volume type must be preserved rather than defaulted to bind.
	m, err := ParseMountSpec("type=volume,source=vol,target=/data,ro")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "volume" || m.Source != "vol" || m.Target != "/data" || !m.ReadOnly {
		t.Errorf("mount = %+v", m)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestCreateContainerArgs_Table asserts the exact `docker create` argument
// slice for discriminating inputs. Ordering is load-bearing: id-labels first,
// then mounts/env/caps/security, flags, runArgs (arbitrary devcontainer.json
// flags) BEFORE the image, then entrypoint, image, and command.
func TestCreateContainerArgs_Table(t *testing.T) {
	tests := []struct {
		name string
		call func() []string
		want []string
	}{
		{
			name: "MinimalImageOnly",
			call: func() []string {
				return CreateContainerArgs("img", nil, nil, nil, "", nil, nil, nil, nil, false, nil, nil)
			},
			want: []string{"create", "img"},
		},
		{
			name: "LabelsAndEnv",
			call: func() []string {
				return CreateContainerArgs("img",
					[]string{"devcontainer.local_folder=/w", "devcontainer.config_file=/w/.devcontainer.json"},
					[]string{"FOO=bar", "BAZ=qux"},
					nil, "", nil, nil, nil, nil, false, nil, nil)
			},
			want: []string{"create",
				"-l", "devcontainer.local_folder=/w",
				"-l", "devcontainer.config_file=/w/.devcontainer.json",
				"-e", "FOO=bar", "-e", "BAZ=qux",
				"img"},
		},
		{
			name: "MountReadOnly",
			call: func() []string {
				return CreateContainerArgs("img", nil, nil,
					[]mount.Mount{{Type: "bind", Source: "/host", Target: "/container", ReadOnly: true}},
					"", nil, nil, nil, nil, false, nil, nil)
			},
			want: []string{"create",
				"--mount", "type=bind,source=/host,target=/container,readonly",
				"img"},
		},
		{
			name: "VolumeMountNoSource",
			call: func() []string {
				return CreateContainerArgs("img", nil, nil,
					[]mount.Mount{{Type: "volume", Target: "/data"}},
					"", nil, nil, nil, nil, false, nil, nil)
			},
			want: []string{"create",
				"--mount", "type=volume,target=/data",
				"img"},
		},
		{
			name: "SecurityCapsPrivilegedInitUser",
			call: func() []string {
				return CreateContainerArgs("img", nil, nil, nil,
					"vscode",
					nil, nil,
					[]string{"SYS_PTRACE", "NET_ADMIN"},
					[]string{"seccomp=unconfined"},
					true, boolPtr(true), nil)
			},
			want: []string{"create",
				"--cap-add", "SYS_PTRACE", "--cap-add", "NET_ADMIN",
				"--security-opt", "seccomp=unconfined",
				"--privileged",
				"--init",
				"-u", "vscode",
				"img"},
		},
		{
			name: "InitFalsePointerOmitsInit",
			call: func() []string {
				return CreateContainerArgs("img", nil, nil, nil, "", nil, nil, nil, nil, false, boolPtr(false), nil)
			},
			want: []string{"create", "img"},
		},
		{
			name: "RunArgsBeforeImageEntrypointAndCmd",
			call: func() []string {
				return CreateContainerArgs("myimage:tag", nil, nil, nil,
					"", []string{"/custom-entrypoint.sh"}, []string{"sleep", "infinity"},
					nil, nil, false, nil,
					[]string{"--network", "host", "--gpus", "all"})
			},
			want: []string{"create",
				"--network", "host", "--gpus", "all",
				"--entrypoint", "/custom-entrypoint.sh",
				"myimage:tag",
				"sleep", "infinity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.call()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateContainerArgs() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestCreateContainerArgs_MountConsistency(t *testing.T) {
	args := CreateContainerArgs("img", nil, nil,
		[]mount.Mount{{Type: "bind", Source: "/a", Target: "/b", Consistency: "cached"}},
		"", nil, nil, nil, nil, false, nil, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "type=bind,source=/a,target=/b,consistency=cached") {
		t.Errorf("args = %q", joined)
	}
}

// FuzzParseMountSpec targets user-controlled --mount strings. Beyond not
// panicking, every accepted result must satisfy the invariants relied on by
// container creation: a destination and an explicit/defaulted mount type.
func FuzzParseMountSpec(f *testing.F) {
	f.Add("type=bind,source=/workspace,target=/workspaces/project,readonly")
	f.Add("type=volume,source=data,destination=/data")
	f.Add("src=/tmp,dst=/tmp/container,ro=true")
	f.Add("target=/default-bind")
	f.Add("type=volume,source=missing-target")

	f.Fuzz(func(t *testing.T, spec string) {
		m, err := ParseMountSpec(spec)
		if err != nil {
			return
		}
		if m.Target == "" {
			t.Fatal("accepted mount has an empty target")
		}
		if m.Type == "" {
			t.Fatal("accepted mount has an empty type")
		}
	})
}
