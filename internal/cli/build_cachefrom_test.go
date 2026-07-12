package cli

import (
	"reflect"
	"testing"

	"github.com/devcontainers/cli/internal/config"
)

// TestCacheFromForDockerfileBuild pins the merge of the --cache-from flag with
// devcontainer.json build.cacheFrom: flag values first, then config values, in
// order — matching TS singleContainer (additionalCacheFroms, then config.build.cacheFrom).
func TestCacheFromForDockerfileBuild(t *testing.T) {
	cfgWith := func(cf ...string) *config.DevContainer {
		return &config.DevContainer{Build: &config.Build{CacheFrom: config.StringOrStrings(cf)}}
	}

	cases := []struct {
		name string
		flag []string
		cfg  *config.DevContainer
		want []string
	}{
		{"nil cfg → flag only", []string{"a"}, nil, []string{"a"}},
		{"no build block → flag only", []string{"a"}, &config.DevContainer{}, []string{"a"}},
		{"empty config cacheFrom → flag only", []string{"a"}, cfgWith(), []string{"a"}},
		{"flag then config (order)", []string{"flag1", "flag2"}, cfgWith("cfg1", "cfg2"), []string{"flag1", "flag2", "cfg1", "cfg2"}},
		{"no flag, config only", nil, cfgWith("cfg1"), []string{"cfg1"}},
		{"single config string", []string{"flag1"}, cfgWith("only"), []string{"flag1", "only"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cacheFromForDockerfileBuild(tc.flag, tc.cfg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("cacheFromForDockerfileBuild(%v, %v) = %v, want %v", tc.flag, tc.cfg, got, tc.want)
			}
		})
	}
}

// TestCacheFromForDockerfileBuildDoesNotMutateFlag guards against the helper
// aliasing/appending into the caller's flag slice (which extendImageWithFeatures
// still passes verbatim for the feature layers).
func TestCacheFromForDockerfileBuildDoesNotMutateFlag(t *testing.T) {
	flag := []string{"flag1"}
	cfg := &config.DevContainer{Build: &config.Build{CacheFrom: config.StringOrStrings{"cfg1"}}}
	_ = cacheFromForDockerfileBuild(flag, cfg)
	if len(flag) != 1 || flag[0] != "flag1" {
		t.Fatalf("flag slice mutated: %v", flag)
	}
}
