//go:build linux

package library

import (
	"errors"

	"golang.org/x/sys/unix"
)

// canWrite asks the kernel whether the real user can create entries in
// dir. It creates nothing, so a crawl of a large tree is cheap.
func canWrite(dir string) bool {
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}

// isCrossDevice reports a rename that failed because source and target
// are on different file systems.
func isCrossDevice(err error) bool {
	return errors.Is(err, unix.EXDEV)
}
