//go:build linux

package audio

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// watchDevSnd calls fn each time a node appears in or disappears from
// /dev/snd. inotify works the same on bare metal and through a Docker bind
// mount, unlike netlink uevents.
func watchDevSnd(ctx context.Context, dir string, fn func()) error {
	// Non-blocking, so the runtime poller owns the fd and Close unblocks
	// the read loop.
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	if _, err := unix.InotifyAddWatch(fd, dir, unix.IN_CREATE|unix.IN_DELETE|unix.IN_ATTRIB); err != nil {
		unix.Close(fd)
		return err
	}
	f := os.NewFile(uintptr(fd), dir)
	go func() {
		<-ctx.Done()
		f.Close()
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				fn()
			}
		}
	}()
	return nil
}
