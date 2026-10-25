package devtools

import (
	"compress/gzip"
	"io"
	"os"
	"time"
)

// Metrics is artifacts/metrics.json: startup time of the Go binary against the
// Node oracle, and the size of the binaries a release ships. A measurement that
// could not be taken is null.
type Metrics struct {
	Version     string     `json:"version"`
	GeneratedAt string     `json:"generated_at"`
	TimingTool  string     `json:"timing_tool"`
	Runs        int        `json:"runs"`
	StartupMs   StartupMs  `json:"startup_ms"`
	SizesBytes  SizesBytes `json:"sizes_bytes"`
}

// StartupMs is the mean wall-clock time of each command, in milliseconds.
type StartupMs struct {
	GoVersion           *float64 `json:"go_version"`
	GoReadConfiguration *float64 `json:"go_read_configuration"`
	NodeVersion         *float64 `json:"node_version"`
}

// SizesBytes is the size of each binary, and of the release binaries gzipped.
type SizesBytes struct {
	LocalBinary      *int64 `json:"local_binary"`
	LinuxAmd64Binary *int64 `json:"linux_amd64_binary"`
	LinuxAmd64Gzip   *int64 `json:"linux_amd64_gzip"`
	LinuxArm64Binary *int64 `json:"linux_arm64_binary"`
	LinuxArm64Gzip   *int64 `json:"linux_arm64_gzip"`
}

// MeanMillis runs once to warm up, then runs times, and returns the mean
// wall-clock milliseconds of a run. A run's failure is measured like a success:
// the former shell loop timed `cmd || true`.
func MeanMillis(runs int, run func()) float64 {
	if runs <= 0 {
		return 0
	}
	run()
	start := time.Now()
	for range runs {
		run()
	}
	return float64(time.Since(start)) / float64(time.Millisecond) / float64(runs)
}

// FileSize is the size of path, or nil when it cannot be read.
func FileSize(path string) *int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return new(fi.Size())
}

// GzipSize is the size of path compressed with gzip's default level, or nil when
// it cannot be read.
func GzipSize(path string) *int64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	cw := &countWriter{}
	zw := gzip.NewWriter(cw)
	if _, err := io.Copy(zw, f); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return new(cw.n)
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
