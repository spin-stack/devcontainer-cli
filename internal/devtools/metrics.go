package devtools

import (
	"compress/gzip"
	"io"
	"os"
	"time"
)

type Metrics struct {
	Version     string     `json:"version"`
	GeneratedAt string     `json:"generated_at"`
	TimingTool  string     `json:"timing_tool"`
	Runs        int        `json:"runs"`
	StartupMs   StartupMs  `json:"startup_ms"`
	SizesBytes  SizesBytes `json:"sizes_bytes"`
}

type StartupMs struct {
	GoVersion           *float64 `json:"go_version"`
	GoReadConfiguration *float64 `json:"go_read_configuration"`
	NodeVersion         *float64 `json:"node_version"`
}

type SizesBytes struct {
	LocalBinary      *int64 `json:"local_binary"`
	LinuxAmd64Binary *int64 `json:"linux_amd64_binary"`
	LinuxAmd64Gzip   *int64 `json:"linux_amd64_gzip"`
	LinuxArm64Binary *int64 `json:"linux_arm64_binary"`
	LinuxArm64Gzip   *int64 `json:"linux_arm64_gzip"`
}

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

func FileSize(path string) *int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return new(fi.Size())
}

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
