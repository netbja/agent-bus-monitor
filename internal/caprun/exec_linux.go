package caprun

import (
	"syscall"
	"unsafe"
)

// execLimited sets RLIMIT_AS (when limit) and execve's, both raw system
// calls on C strings built beforehand: nothing between them allocates. It
// returns only on a failure, with the step that failed.
func execLimited(c cExec, limit bool, lim *syscall.Rlimit) (string, error) {
	if limit {
		if _, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, 0, syscall.RLIMIT_AS, uintptr(unsafe.Pointer(lim)), 0, 0, 0); e != 0 {
			return "memory limit", e
		}
	}
	_, _, e := syscall.RawSyscall(syscall.SYS_EXECVE, uintptr(unsafe.Pointer(c.cpath)), uintptr(unsafe.Pointer(&c.cargv[0])), uintptr(unsafe.Pointer(&c.cenvv[0])))
	return "exec", e
}
