package devtools

import "syscall"

// RunnerDiskRoom is the free space on / the runtime parity lane needs: its cases'
// images, the reference oracle's and the build cache exports. GitHub's runners
// start with about 20 GB free and get there by deleting preinstalled SDKs; the
// docker8 fleet's 150 GB volume starts past it.
const RunnerDiskRoom = 60 << 30

// NeedsDiskFreeing reports whether a runner with free bytes on / has to delete
// its preinstalled SDKs to make RunnerDiskRoom.
func NeedsDiskFreeing(free uint64) bool {
	return free < RunnerDiskRoom
}

// FreeDisk returns the bytes free on the filesystem holding path.
func FreeDisk(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
