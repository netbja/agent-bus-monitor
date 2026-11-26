//go:build !linux

package caprun

import "syscall"

// execLimited outside Linux: RLIMIT_AS is not enforced there (macOS), so the
// runtime cannot trip on it, and syscall.Exec builds its own C strings.
func execLimited(c cExec, limit bool, lim *syscall.Rlimit) (string, error) {
	if limit {
		if err := syscall.Setrlimit(syscall.RLIMIT_AS, lim); err != nil {
			return "memory limit", err
		}
	}
	return "exec", syscall.Exec(c.path, c.argv, c.envv)
}
