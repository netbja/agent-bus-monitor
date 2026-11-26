package caprun

import (
	"syscall"
	"unsafe"
)

// execLimited sets RLIMIT_AS (when limit) and execve's, both raw system
// calls on C strings built beforehand: nothing between them allocates. It
// returns only on a failure, with the step that failed.
//
// Two things syscall.Exec does are skipped (foureyes, step 2a):
//   - the runtime's exec lock, which stops it creating a thread during the
//     exec: the helper runs nothing else, and an execve that fails returns
//     here and is reported (run exits 126);
//   - restoring the RLIMIT_NOFILE soft limit Go raised at start (its
//     original is unexported): the capped command starts with its soft
//     open-file limit raised toward the hard one (1024 became 524287 here),
//     where an uncapped run gets the original. Go programs raise it
//     themselves; only an old select()-based program could tell.
func execLimited(c cExec, limit bool, lim *syscall.Rlimit) (string, error) {
	if limit {
		if _, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, 0, syscall.RLIMIT_AS, uintptr(unsafe.Pointer(lim)), 0, 0, 0); e != 0 {
			return "memory limit", e
		}
	}
	_, _, e := syscall.RawSyscall(syscall.SYS_EXECVE, uintptr(unsafe.Pointer(c.cpath)), uintptr(unsafe.Pointer(&c.cargv[0])), uintptr(unsafe.Pointer(&c.cenvv[0])))
	return "exec", e
}
