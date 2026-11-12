package devtools

func DailyChanged(lastCommitEpoch, nowEpoch, windowHours int) bool {
	ageHours := (nowEpoch - lastCommitEpoch) / 3600
	return ageHours < windowHours
}
