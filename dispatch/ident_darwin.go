//go:build darwin

package dispatch

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// procStat reads pid from the process table (sysctl kern.proc.pid; macOS has
// no /proc, self-repair step 10): its parent, and its start time in
// microseconds since the epoch, fixed for the process's life. The table
// lists no such pid (the sysctl returns no record: SysctlKinfoProc's EIO, or
// a record of another pid) is ErrNoProcess; any other failure proves
// nothing.
func procStat(pid int) (statFields, error) {
	if pid <= 0 {
		return statFields{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	switch {
	case errors.Is(err, unix.EIO), err == nil && int(k.Proc.P_pid) != pid:
		return statFields{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	case err != nil:
		return statFields{}, err
	}
	t := k.Proc.P_starttime
	start := uint64(t.Sec)*1_000_000 + uint64(t.Usec)
	if start == 0 {
		return statFields{}, fmt.Errorf("pid %d: no start time", pid)
	}
	return statFields{ppid: int(k.Eproc.Ppid), start: start}, nil
}

func bootID() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }
