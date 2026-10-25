package docker

import "testing"

// A service built without an image is tagged "<project>-<service>", and
// "<project>_<service>" by Compose before 2.8.0, as TS getDefaultImageName
// decides with isEarlierVersion(version, [2, 8, 0]): a missing version
// component counts as 0, and an unknown version is a current Compose.
func TestDefaultImageNameFollowsTheComposeVersion(t *testing.T) {
	for _, tc := range []struct {
		version []int
		want    string
	}{
		{nil, "proj-app"},
		{[]int{1}, "proj_app"},
		{[]int{1, 29, 2}, "proj_app"},
		{[]int{2}, "proj_app"},
		{[]int{2, 7, 9}, "proj_app"},
		{[]int{2, 8}, "proj-app"},
		{[]int{2, 8, 0}, "proj-app"},
		{[]int{2, 8, 1}, "proj-app"},
		{[]int{2, 10}, "proj-app"},
		{[]int{3}, "proj-app"},
	} {
		c := &ComposeClient{Version: tc.version}
		if got := c.DefaultImageName("proj", "app"); got != tc.want {
			t.Errorf("DefaultImageName with Compose %v = %q, want %q", tc.version, got, tc.want)
		}
	}
}
