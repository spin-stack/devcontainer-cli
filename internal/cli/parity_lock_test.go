package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	parityWorkspaceFlag  = regexp.MustCompile(`--workspace-folder\s+(src/test/configs/[^\s"';]+)`)
	parityConfigFlag     = regexp.MustCompile(`--config\s+(src/test/configs/[^\s"';]+)`)
	nonEmptyFeaturesJSON = regexp.MustCompile(`"features"\s*:\s*\{\s*"`)
	setupWritesFeatures  = regexp.MustCompile(`\\?"features\\?"\s*:\s*\{\s*\\?"`)
)

func TestParityMatrixLocksTheFeatureContentImage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "parity", "parity-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix parityMatrix
	if err := yaml.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	locked := 0
	for _, tc := range matrix.InitialCases {
		if _, ok := parityLocks[tc.Lock]; tc.Lock != "" && !ok {
			t.Errorf("case %q names unknown lock %q", tc.ID, tc.Lock)
		}
		cmds := tc.TSCmd + "\n" + tc.SetupCmd
		need := strings.Contains(cmds, "--buildkit=never") && (setupWritesFeatures.MatchString(tc.SetupCmd) || slices.ContainsFunc(parityCaseConfigs(t, cmds), func(config string) bool {
			data, err := os.ReadFile(filepath.Join("..", "..", config))
			if err != nil {
				t.Fatalf("case %q: %v", tc.ID, err)
			}
			return nonEmptyFeaturesJSON.Match(data)
		}))
		switch has := tc.Lock == "feature-content-image"; {
		case need && !has:
			t.Errorf("case %q builds Features without BuildKit; add `lock: feature-content-image`", tc.ID)
		case has && !need:
			t.Errorf("case %q holds feature-content-image but builds no Features without BuildKit", tc.ID)
		case has:
			locked++
		}
	}
	if locked == 0 {
		t.Error("no case holds feature-content-image")
	}
}

func parityCaseConfigs(t *testing.T, cmds string) []string {
	var configs []string
	for _, m := range parityConfigFlag.FindAllStringSubmatch(cmds, -1) {
		configs = append(configs, m[1])
	}
	for _, m := range parityWorkspaceFlag.FindAllStringSubmatch(cmds, -1) {
		for _, c := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
			if _, err := os.Stat(filepath.Join("..", "..", m[1], c)); err == nil {
				configs = append(configs, filepath.Join(m[1], c))
				break
			}
		}
	}
	return configs
}
