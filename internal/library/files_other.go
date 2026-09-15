//go:build !linux

package library

import (
	"errors"
	"syscall"
)

// errNotSameDevice is Windows ERROR_NOT_SAME_DEVICE, the rename error a
// developer machine reports across drives.
const errNotSameDevice = syscall.Errno(17)

func canWrite(dir string) bool { return Writable(dir) }

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV) || errors.Is(err, errNotSameDevice)
}
