package holder

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// becomeSubreaper makes the holder its descendants' reaper (spec 4.2 and
// 4.4): an orphaned or double-forked process stays its descendant, so the
// in-tree check still sees it, and its exit is reaped here.
func becomeSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// reapLoop reaps, on each SIGCHLD, the holder's children it did not start
// (orphans it inherited as a subreaper); a terminal's own pid is left to its
// Wait, so its exit status is observed. Never wait(-1): it would race those
// Waits (spec 4.4). tracked is read under the server's lock, which Start
// holds from the fork until the pid is recorded.
func reapLoop(stop <-chan struct{}, tracked func(pid int) bool) {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGCHLD)
	defer signal.Stop(ch)
	self := os.Getpid()
	sweep := time.NewTicker(5 * time.Second) // a coalesced or missed SIGCHLD: swept anyway
	defer sweep.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ch:
		case <-sweep.C:
		}
		reapSweep(self, tracked, childrenOf, readProcStat, func(pid int) {
			var ws unix.WaitStatus
			_, _ = unix.Wait4(pid, &ws, unix.WNOHANG, nil)
		})
	}
}

// reapSweep reaps self's exited children that are not a terminal's program:
// a tracked pid is left to its own Wait (its exit status is the terminal's),
// a running orphan is reaped at its own exit.
func reapSweep(self int, tracked func(int) bool, children func(int) []int, stat func(int) (procStat, bool), reap func(int)) {
	for _, pid := range children(self) {
		if tracked(pid) {
			continue
		}
		if st, ok := stat(pid); ok && st.state == 'Z' {
			reap(pid)
		}
	}
}
