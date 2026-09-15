//go:build linux

package mpdctl

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processStartTime returns field 22 of /proc/<pid>/stat, the start time in
// clock ticks since boot. A recycled PID has a different value.
func processStartTime(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	// The command name in parentheses may hold spaces; fields start after
	// the last ')'.
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(data[i+1:]))
	// fields[0] is field 3 (state), so field 22 is fields[19].
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// processAlive reports whether a process with this pid exists.
func processAlive(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// processOwnedByMe reports whether /proc/<pid> belongs to this uid.
func processOwnedByMe(pid int) bool {
	st, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}

// recoverOrphan kills an MPD that a killed or panicked jukem left behind.
// It kills a process only when every identity check passes, so it never
// touches a recycled PID.
func recoverOrphan(paths Paths, binary string, log *slog.Logger) {
	exe := resolveBinary(binary)
	if rec, err := readPIDFile(paths.PIDFile); err == nil {
		if isOurMPD(rec.PID, rec.StartTime, exe) {
			log.Warn("killing orphaned mpd from a previous run", "pid", rec.PID)
			killAndWait(rec.PID)
		}
	}
	os.Remove(paths.PIDFile)
	// Backstop for a missing pid file: a process of ours that runs on the
	// generated config path.
	for _, pid := range findByCmdline(paths.ConfFile) {
		if processOwnedByMe(pid) {
			log.Warn("killing orphaned mpd found by config path", "pid", pid)
			killAndWait(pid)
		}
	}
}

func resolveBinary(binary string) string {
	p, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// isOurMPD checks pid, start time, executable and owner together.
func isOurMPD(pid int, startTime, exe string) bool {
	if pid <= 1 || !processAlive(pid) {
		return false
	}
	if startTime == "" || processStartTime(pid) != startTime {
		return false
	}
	if !processOwnedByMe(pid) {
		return false
	}
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false
	}
	link = strings.TrimSuffix(link, " (deleted)")
	return exe != "" && link == exe
}

// findByCmdline returns pids whose command line names confFile.
func findByCmdline(confFile string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	me := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == me {
			continue
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		args := strings.Split(string(data), "\x00")
		for _, a := range args[1:] {
			if a == confFile {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// killAndWait sends SIGTERM, SIGKILLs after five seconds, and returns once
// the process is gone, so the next MPD does not find the device busy.
func killAndWait(pid int) {
	syscall.Kill(pid, syscall.SIGTERM)
	if waitGone(pid, 5*time.Second) {
		return
	}
	syscall.Kill(pid, syscall.SIGKILL)
	waitGone(pid, 2*time.Second)
}

func waitGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !processAlive(pid)
}
