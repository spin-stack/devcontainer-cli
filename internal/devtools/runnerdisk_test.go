package devtools

import "testing"

// A runner with less than RunnerDiskRoom free deletes its SDKs (GitHub's, about
// 20 GB free); one with room keeps them (the docker8 fleet's 150 GB volume).
func TestNeedsDiskFreeing(t *testing.T) {
	tests := []struct {
		name string
		free uint64
		want bool
	}{
		{"github runner", 20 << 30, true},
		{"one byte short", RunnerDiskRoom - 1, true},
		{"exactly the room", RunnerDiskRoom, false},
		{"fleet volume", 140 << 30, false},
	}
	for _, tt := range tests {
		if got := NeedsDiskFreeing(tt.free); got != tt.want {
			t.Errorf("%s: NeedsDiskFreeing(%d) = %v, want %v", tt.name, tt.free, got, tt.want)
		}
	}
}

func TestFreeDiskReadsTheFilesystem(t *testing.T) {
	free, err := FreeDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("FreeDisk = 0 on a writable temp dir")
	}
	if _, err := FreeDisk("/nonexistent/path"); err == nil {
		t.Fatal("FreeDisk on a missing path: want an error")
	}
}
