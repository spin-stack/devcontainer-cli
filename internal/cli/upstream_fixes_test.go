package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
)

func mountOrString(t *testing.T, jsonLit string) config.MountOrString {
	t.Helper()
	var m config.MountOrString
	if err := json.Unmarshal([]byte(jsonLit), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMetadataMounts_ReadonlyObject(t *testing.T) {
	cfg := &config.DevContainer{
		Mounts: []config.MountOrString{
			mountOrString(t, `{"type":"bind","source":"/local","target":"/remote","readonly":true}`),
		},
	}
	entries := metadataMounts(cfg)
	if len(entries) != 1 {
		t.Fatalf("want 1 mount entry, got %d", len(entries))
	}
	m, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("mount entry is %T, want map", entries[0])
	}
	if ro, _ := m["readonly"].(bool); !ro {
		t.Fatalf("#881 readonly not serialized: %v", m)
	}

	mounts, err := mountsFromMetadata(entries, "devid")
	if err != nil {
		t.Fatal(err)
	}
	args := docker.CreateContainerArgs("img", nil, nil, mounts, "", nil, nil, nil, nil, false, nil, nil)
	if !strings.Contains(strings.Join(args, " "), "target=/remote,readonly") {
		t.Errorf("#881 docker --mount missing readonly: %v", args)
	}

	composeMounts, err := composeMountsFromMetadata(entries, func(s string) (string, error) { return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(composeMounts) != 1 || !strings.HasSuffix(composeVolumeSpec(composeMounts[0]), ":ro") {
		t.Errorf("#881 compose spec missing :ro: %v", composeMounts)
	}
}

func TestMetadataMounts_ReadonlyOmittedByDefault(t *testing.T) {
	cfg := &config.DevContainer{
		Mounts: []config.MountOrString{
			mountOrString(t, `{"type":"bind","source":"/local","target":"/remote"}`),
		},
	}
	m := metadataMounts(cfg)[0].(map[string]any)
	if _, present := m["readonly"]; present {
		t.Errorf("#881 readonly must be omitted when unset: %v", m)
	}
}

func TestCheckGPUAvailability_NvidiaCLIProbe(t *testing.T) {
	orig := nvidiaContainerCLIInfo
	t.Cleanup(func() { nvidiaContainerCLIInfo = orig })
	ctx := context.Background()

	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return true, false }
	if detectGPU(ctx, true) {
		t.Error("#319 GPU enabled despite nvidia-container-cli reporting no GPU")
	}
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return true, true }
	if !detectGPU(ctx, true) {
		t.Error("#319 GPU not enabled despite a present GPU")
	}
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return false, false }
	if !detectGPU(ctx, true) {
		t.Error("#319 without nvidia-container-cli, must keep TS runtime-only behavior")
	}
	if detectGPU(ctx, false) {
		t.Error("no nvidia runtime in docker info -> GPU must stay disabled")
	}
}

func TestBuildPositionalPath(t *testing.T) {
	cmd := newBuildCmd()
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("#8 build must reject more than one positional arg")
	}
	if err := cmd.Args(cmd, []string{"only"}); err != nil {
		t.Errorf("#8 build must accept a single positional arg: %v", err)
	}
}

func TestMountFromMetadata_DevcontainerIdInTarget(t *testing.T) {
	m, err := mountFromMetadata(map[string]any{
		"type":   "volume",
		"source": "cache-${devcontainerId}",
		"target": "/cache/${devcontainerId}",
	}, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != "cache-abc123" {
		t.Errorf("source = %q, want cache-abc123", m.Source)
	}
	if m.Target != "/cache/abc123" {
		t.Errorf("target = %q, want /cache/abc123 (was left literal)", m.Target)
	}
}
