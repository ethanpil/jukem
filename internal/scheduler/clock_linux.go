//go:build linux

package scheduler

import (
	"os"

	"golang.org/x/sys/unix"
)

// probeClock asks the kernel whether the clock is synchronised, which
// works inside a container too, and looks for a hardware clock. The
// synchronised flag is cleared only by an NTP daemon, so an RTC-only box
// reports unsynchronised with a correct time.
func probeClock() (ntpSynced bool, rtcPresent bool) {
	var tx unix.Timex
	if state, err := unix.Adjtimex(&tx); err == nil && state != unix.TIME_ERROR {
		ntpSynced = tx.Status&unix.STA_UNSYNC == 0
	}
	_, err := os.Stat("/sys/class/rtc/rtc0")
	return ntpSynced, err == nil
}
