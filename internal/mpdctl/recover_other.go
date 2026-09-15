//go:build !linux

package mpdctl

import (
	"log/slog"
	"os"
)

// The appliance runs on Linux. These stubs keep the package building on a
// developer machine.

func processStartTime(pid int) string { return "" }

func recoverOrphan(paths Paths, binary string, log *slog.Logger) {
	os.Remove(paths.PIDFile)
}
