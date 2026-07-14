package devtools

import "testing"

// TestDailyChanged covers the run/skip boundary of the daily gate: ages use
// integer-hour truncation, and the window is exclusive (age == window skips),
// matching the former daily-changed.sh.
func TestDailyChanged(t *testing.T) {
	const now = 2_000_000_000
	const h = 3600
	cases := []struct {
		name    string
		ageSecs int
		window  int
		want    bool
	}{
		{"fresh 1h", 1 * h, 25, true},
		{"23h59m still 23h", 23*h + 3599, 25, true},
		{"exactly 24h", 24 * h, 25, true},
		{"24h59m truncates to 24", 24*h + 3599, 25, true},
		{"exactly 25h == window skips", 25 * h, 25, false},
		{"26h skips", 26 * h, 25, false},
		{"48h skips", 48 * h, 25, false},
		{"wide window runs 30h", 30 * h, 72, true},
		{"narrow window skips 2h", 2 * h, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DailyChanged(now-tc.ageSecs, now, tc.window); got != tc.want {
				t.Errorf("DailyChanged(age=%ds, window=%dh) = %v, want %v", tc.ageSecs, tc.window, got, tc.want)
			}
		})
	}
}
