package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
)

// mountOrString builds a config.MountOrString from a JSON literal (its internal
// representation is unexported, so this also exercises the unmarshal path).
func mountOrString(t *testing.T, jsonLit string) config.MountOrString {
	t.Helper()
	var m config.MountOrString
	if err := json.Unmarshal([]byte(jsonLit), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestMetadataMounts_ReadonlyObject covers upstream #881: a top-level mount
// OBJECT with readonly:true must carry the flag through so both the docker and
// compose renderers emit a read-only mount (previously the struct dropped it).
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
	m, ok := entries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("mount entry is %T, want map", entries[0])
	}
	if ro, _ := m["readonly"].(bool); !ro {
		t.Fatalf("#881 readonly not serialized: %v", m)
	}

	// docker path: CreateContainerArgs must render `readonly` in the --mount spec.
	mounts, err := mountsFromMetadata(entries, "devid")
	if err != nil {
		t.Fatal(err)
	}
	args := docker.CreateContainerArgs("img", nil, nil, mounts, "", nil, nil, nil, nil, false, nil, nil)
	if !strings.Contains(strings.Join(args, " "), "target=/remote,readonly") {
		t.Errorf("#881 docker --mount missing readonly: %v", args)
	}

	// compose path: the volume spec must end in :ro.
	specs, _, err := composeVolumeSpecsFromMetadata(entries, "devid")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || !strings.HasSuffix(specs[0], ":ro") {
		t.Errorf("#881 compose spec missing :ro: %v", specs)
	}
}

// TestMetadataMounts_ReadonlyOmittedByDefault guards that a mount without the
// property does not spuriously become read-only.
func TestMetadataMounts_ReadonlyOmittedByDefault(t *testing.T) {
	cfg := &config.DevContainer{
		Mounts: []config.MountOrString{
			mountOrString(t, `{"type":"bind","source":"/local","target":"/remote"}`),
		},
	}
	m := metadataMounts(cfg)[0].(map[string]interface{})
	if _, present := m["readonly"]; present {
		t.Errorf("#881 readonly must be omitted when unset: %v", m)
	}
}

// TestCheckGPUAvailability_NvidiaCLIProbe covers upstream #319: when the nvidia
// runtime is reported by docker info but nvidia-container-cli reports NO GPU
// (driver loaded, no device), detect mode must NOT enable the GPU.
func TestCheckGPUAvailability_NvidiaCLIProbe(t *testing.T) {
	// checkGPUAvailability's detect path needs a docker info result; without a
	// real daemon we can only exercise the "all"/"none" shortcuts here plus the
	// probe override in isolation. Verify the probe override is honored by the
	// detect branch's decision function.
	orig := nvidiaContainerCLIInfo
	t.Cleanup(func() { nvidiaContainerCLIInfo = orig })
	ctx := context.Background()

	// Tool present, no GPU -> the refined signal wins (false).
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return true, false }
	if detectGPU(ctx, true) {
		t.Error("#319 GPU enabled despite nvidia-container-cli reporting no GPU")
	}
	// Tool present, GPU present -> true.
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return true, true }
	if !detectGPU(ctx, true) {
		t.Error("#319 GPU not enabled despite a present GPU")
	}
	// Tool absent -> fall back to the docker-info runtime signal (TS parity).
	nvidiaContainerCLIInfo = func(context.Context) (bool, bool) { return false, false }
	if !detectGPU(ctx, true) {
		t.Error("#319 without nvidia-container-cli, must keep TS runtime-only behavior")
	}
	if detectGPU(ctx, false) {
		t.Error("no nvidia runtime in docker info -> GPU must stay disabled")
	}
}

// TestBuildPositionalPath covers upstream #8: the advertised [path] positional
// is honored as the workspace folder when --workspace-folder is not set.
func TestBuildPositionalPath(t *testing.T) {
	cmd := newBuildCmd()
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("#8 build must reject more than one positional arg")
	}
	if err := cmd.Args(cmd, []string{"only"}); err != nil {
		t.Errorf("#8 build must accept a single positional arg: %v", err)
	}
}
