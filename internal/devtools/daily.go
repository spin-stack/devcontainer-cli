package devtools

// DailyChanged decides whether the daily scheduled parity run should do the heavy
// Docker work: true iff HEAD's commit is within the look-back window (the repo
// changed since the previous daily run). Ages use integer-hour truncation to
// match the former daily-changed.sh ($(( (now-last)/3600 ))). Ported from
// scripts/daily-changed.sh.
func DailyChanged(lastCommitEpoch, nowEpoch, windowHours int) bool {
	ageHours := (nowEpoch - lastCommitEpoch) / 3600
	return ageHours < windowHours
}
