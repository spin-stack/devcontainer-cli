package devtools

import "syscall"

const RunnerDiskRoom = 60 << 30

func NeedsDiskFreeing(free uint64) bool {
	return free < RunnerDiskRoom
}

func FreeDisk(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
