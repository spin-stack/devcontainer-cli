package devtools

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// metrics.json keeps the shape the former shell wrote: every key present, and a
// measurement that was not taken written as null, not omitted.
func TestMetricsJSONKeepsEveryKeyAndNulls(t *testing.T) {
	ms := 1.5
	size := int64(10)
	m := Metrics{Version: "v1", GeneratedAt: "2026-10-25T00:00:00Z", TimingTool: "go", Runs: 3,
		StartupMs:  StartupMs{GoVersion: &ms},
		SizesBytes: SizesBytes{LocalBinary: &size}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":"v1","generated_at":"2026-10-25T00:00:00Z","timing_tool":"go","runs":3,` +
		`"startup_ms":{"go_version":1.5,"go_read_configuration":null,"node_version":null},` +
		`"sizes_bytes":{"local_binary":10,"linux_amd64_binary":null,"linux_amd64_gzip":null,"linux_arm64_binary":null,"linux_arm64_gzip":null}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// MeanMillis warms up once and then times exactly runs calls.
func TestMeanMillisWarmsUpThenRuns(t *testing.T) {
	calls := 0
	if got := MeanMillis(4, func() { calls++ }); got < 0 {
		t.Errorf("mean = %v, want >= 0", got)
	}
	if calls != 5 {
		t.Errorf("calls = %d, want 5 (1 warmup + 4 runs)", calls)
	}
	if got := MeanMillis(0, func() { t.Fatal("ran with runs=0") }); got != 0 {
		t.Errorf("runs=0 mean = %v, want 0", got)
	}
	calls = 0
	MeanMillis(1, func() { calls++ })
	if calls != 2 {
		t.Errorf("runs=1: calls = %d, want 2 (1 warmup + 1 run)", calls)
	}
}

// The mean is in milliseconds: a run of 20 ms measures as 20 ms, not as its
// microseconds or seconds.
func TestMeanMillisIsMilliseconds(t *testing.T) {
	got := MeanMillis(2, func() { time.Sleep(20 * time.Millisecond) })
	if got < 20 || got > 2000 {
		t.Errorf("mean = %v ms for 20 ms runs", got)
	}
}

// Sizes are nil for a missing file, and the gzip size is what gzip writes.
func TestFileAndGzipSize(t *testing.T) {
	dir := t.TempDir()
	if FileSize(filepath.Join(dir, "absent")) != nil || GzipSize(filepath.Join(dir, "absent")) != nil {
		t.Fatal("a missing file has no size")
	}
	data := bytes.Repeat([]byte("devcontainer "), 1000)
	p := filepath.Join(dir, "bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FileSize(p); got == nil || *got != int64(len(data)) {
		t.Errorf("FileSize = %v, want %d", got, len(data))
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	if got := GzipSize(p); got == nil || *got != int64(buf.Len()) {
		t.Errorf("GzipSize = %v, want %d", got, buf.Len())
	}
}
