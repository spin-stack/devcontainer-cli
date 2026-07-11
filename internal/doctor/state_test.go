package doctor

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "check.json")
	want := Report{
		SchemaVersion: schemaVersion,
		CLIVersion:    "1.2.3",
		CheckedAt:     time.Now().UTC().Truncate(time.Second),
		Overall:       StatusWarn,
		Results: []Result{
			{Name: "docker-daemon", Status: StatusOK, Summary: "ok"},
			{Name: "build-cache-export", Status: StatusWarn, Summary: "no export", Remediation: "run setup", Fixable: true},
		},
	}
	if err := SaveTo(path, want); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, ok, err := LoadFrom(path)
	if err != nil || !ok {
		t.Fatalf("LoadFrom: ok=%v err=%v", ok, err)
	}
	if got.Overall != StatusWarn || len(got.Results) != 2 || !got.Results[1].Fixable {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Results[0].Status != StatusOK || got.Results[1].Status != StatusWarn {
		t.Fatalf("status round-trip wrong: %+v", got.Results)
	}
}

func TestLoadMissingIsNotAnError(t *testing.T) {
	_, ok, err := LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if ok || err != nil {
		t.Fatalf("LoadFrom(absent) = ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestStatePathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	p, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/xdg/state", "devcontainer", "check.json") {
		t.Fatalf("StatePath = %q", p)
	}
}

func TestStatePathHomeFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	p, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/home/tester", ".local", "state", "devcontainer", "check.json") {
		t.Fatalf("StatePath = %q", p)
	}
}
