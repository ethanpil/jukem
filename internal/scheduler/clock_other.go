//go:build !linux

package scheduler

// probeClock trusts a developer machine's clock as if it were RTC-backed.
func probeClock() (ntpSynced bool, rtcPresent bool) { return false, true }
