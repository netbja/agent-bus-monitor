package dispatch

import "fmt"

// kinfoVerdict judges macOS's process table answer for pid (sysctl
// kern.proc.pid, read as records): nil for exactly one record of pid,
// ErrNoProcess for none, any other answer an error that proves nothing — a
// failed or malformed read, several records, a record of another pid. Only
// zero records proves the process gone (reviewer-codex: SysctlKinfoProc's
// EIO also stands for a size mismatch). Pure, so it is tested on every
// system.
func kinfoVerdict(pid int, recordPIDs []int, err error) error {
	switch {
	case err != nil:
		return fmt.Errorf("pid %d: the process table: %w", pid, err)
	case len(recordPIDs) == 0:
		return fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	case len(recordPIDs) > 1:
		return fmt.Errorf("pid %d: the process table answered %d records", pid, len(recordPIDs))
	case recordPIDs[0] != pid:
		return fmt.Errorf("pid %d: the process table answered pid %d", pid, recordPIDs[0])
	}
	return nil
}
