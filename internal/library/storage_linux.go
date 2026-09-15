//go:build linux

package library

import "golang.org/x/sys/unix"

// diskSpace returns the total and available bytes of the file system that
// holds dir. Available is what an unprivileged process can use.
func diskSpace(dir string) (total, free int64) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	return int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize)
}
