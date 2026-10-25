package devtools

import (
	"errors"
	"slices"
	"testing"
)

// TestResolveRelease covers what the former release:resolve shell decided: a tag
// push releases its tag; a dispatch releases its input (one leading v dropped) or
// the day's next CalVer, rejects a version outside [0-9A-Za-z.+-], and refuses a
// tag that already exists.
func TestResolveRelease(t *testing.T) {
	exists := func(tags ...string) func(string) bool {
		return func(tag string) bool {
			return slices.Contains(tags, tag)
		}
	}
	base := ReleaseInput{HeadSHA: "head", Today: "20261025", TagExists: exists()}
	cases := []struct {
		name    string
		in      func(ReleaseInput) ReleaseInput
		want    Release
		wantErr error
	}{
		{"tag push", func(i ReleaseInput) ReleaseInput {
			i.Event, i.RefName, i.PushedSHA = "push", "v20261024.03", "pushed"
			return i
		}, Release{"20261024.03", "v20261024.03", "pushed"}, nil},
		{"dispatch input with v", func(i ReleaseInput) ReleaseInput {
			i.Event, i.VersionInput = "workflow_dispatch", "v1.2.3"
			return i
		}, Release{"1.2.3", "v1.2.3", "head"}, nil},
		{"only one leading v is dropped", func(i ReleaseInput) ReleaseInput {
			i.VersionInput = "vv1"
			return i
		}, Release{"v1", "vv1", "head"}, nil},
		{"outside CI, first CalVer of the day", func(i ReleaseInput) ReleaseInput { return i },
			Release{"20261025.01", "v20261025.01", "head"}, nil},
		{"next CalVer after two tags today", func(i ReleaseInput) ReleaseInput {
			i.TagsToday = 2
			return i
		}, Release{"20261025.03", "v20261025.03", "head"}, nil},
		{"build metadata and prerelease are plain tokens", func(i ReleaseInput) ReleaseInput {
			i.VersionInput = "1.0.0-rc.1+b2"
			return i
		}, Release{"1.0.0-rc.1+b2", "v1.0.0-rc.1+b2", "head"}, nil},
		{"shell metacharacters rejected", func(i ReleaseInput) ReleaseInput {
			i.VersionInput = "1.0;rm -rf"
			return i
		}, Release{}, ErrInvalidVersion},
		{"a lone v is empty after the trim and gets the CalVer", func(i ReleaseInput) ReleaseInput {
			i.VersionInput = "v"
			return i
		}, Release{"20261025.01", "v20261025.01", "head"}, nil},
		{"existing tag refused", func(i ReleaseInput) ReleaseInput {
			i.VersionInput, i.TagExists = "1.2.3", exists("v1.2.3")
			return i
		}, Release{}, ErrTagExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveRelease(tc.in(base))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
