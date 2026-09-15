//go:build !linux

package library

import (
	"errors"
	"syscall"
)

func canWrite(dir string) bool { return Writable(dir) }

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.Errno(17))
}
