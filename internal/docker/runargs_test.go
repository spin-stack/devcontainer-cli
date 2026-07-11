package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/mount"
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

func TestCreateContainerArgs_MountConsistency(t *testing.T) {
	args := CreateContainerArgs("img", nil, nil,
		[]mount.Mount{{Type: "bind", Source: "/a", Target: "/b", Consistency: "cached"}},
		"", nil, nil, nil, nil, false, nil, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "type=bind,source=/a,target=/b,consistency=cached") {
		t.Errorf("args = %q", joined)
	}
}
