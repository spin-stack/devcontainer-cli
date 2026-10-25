package cli

import (
	"testing"
)

// The mounts of image metadata are exactly its entries, in order, each with
// ${devcontainerId} resolved; an invalid entry fails them all.
func TestMountsFromMetadata(t *testing.T) {
	got, err := mountsFromMetadata([]any{
		"type=volume,source=a-${devcontainerId},target=/a",
		map[string]any{"type": "bind", "source": "/b", "target": "/b"},
	}, "id1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Source != "a-id1" || got[0].Target != "/a" || got[1].Source != "/b" || got[1].Target != "/b" {
		t.Errorf("mounts = %+v, want a-id1 -> /a then /b -> /b", got)
	}
	if got, err := mountsFromMetadata(nil, "id1"); err != nil || len(got) != 0 {
		t.Errorf("no entries: %+v, %v; want none", got, err)
	}
	if _, err := mountsFromMetadata([]any{"type=volume,target=/a", map[string]any{"source": "/no-target"}}, "id1"); err == nil {
		t.Error("an invalid entry: want an error")
	}
}
