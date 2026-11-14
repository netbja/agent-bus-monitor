//go:build darwin

package dispatch

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// procStat reads pid from the process table (sysctl kern.proc.pid; macOS has
// no /proc, self-repair step 10): its parent, and its start time in
// microseconds since the epoch, fixed for the process's life. The answer is
// read as records (SysctlKinfoProcSlice), so zero records — the one proof of
// a process gone, ErrNoProcess — is told from a malformed answer, which
// proves nothing (kinfoVerdict).
func procStat(pid int) (statFields, error) {
	if pid <= 0 {
		return statFields{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	ks, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	pids := make([]int, len(ks))
	for i := range ks {
		pids[i] = int(ks[i].Proc.P_pid)
	}
	if err := kinfoVerdict(pid, pids, err); err != nil {
		return statFields{}, err
	}
	t := ks[0].Proc.P_starttime
	start := uint64(t.Sec)*1_000_000 + uint64(t.Usec)
	if start == 0 {
		return statFields{}, fmt.Errorf("pid %d: no start time", pid)
	}
	return statFields{ppid: int(ks[0].Eproc.Ppid), start: start}, nil
}

func bootID() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }
