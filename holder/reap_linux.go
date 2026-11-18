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
// holds from the fork until the pid is recorded. An untracked zombie is
// reaped only after orphanGrace: a command this process runs itself (os/exec)
// is reaped by its own Wait within that time, which a reap at first sight
// raced ("waitid: no child processes", the release check's flake).
func reapLoop(stop <-chan struct{}, tracked func(pid int) bool) {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGCHLD)
	defer signal.Stop(ch)
	self := os.Getpid()
	sweep := time.NewTicker(time.Second) // an orphan's grace ends without a SIGCHLD: swept anyway
	defer sweep.Stop()
	r := newOrphanReaper(orphanGrace)
	for {
		select {
		case <-stop:
			return
		case <-ch:
		case <-sweep.C:
		}
		r.sweep(self, tracked, childrenOf, readProcStat, func(pid int) {
			var ws unix.WaitStatus
			_, _ = unix.Wait4(pid, &ws, unix.WNOHANG, nil)
		}, time.Now())
	}
}

// orphanGrace is how long an untracked child stays a zombie before the
// reaper takes it: far longer than any Wait of this process takes to reap
// its own command once it exits.
const orphanGrace = 2 * time.Second

// zombieKey names one process: its pid and start time (a pid is reused).
type zombieKey struct {
	pid   int
	start uint64
}

// orphanReaper remembers when each untracked zombie was first seen.
type orphanReaper struct {
	grace time.Duration
	seen  map[zombieKey]time.Time
}

func newOrphanReaper(grace time.Duration) *orphanReaper {
	return &orphanReaper{grace: grace, seen: map[zombieKey]time.Time{}}
}

// sweep reaps self's exited children that are not a terminal's program and
// have stayed zombies for the grace: a tracked pid is left to its own Wait
// (its exit status is the terminal's), a running orphan is reaped after its
// exit, a zombie first seen now waits for the grace. A zombie no longer seen
// (its own Wait reaped it) is forgotten.
func (r *orphanReaper) sweep(self int, tracked func(int) bool, children func(int) []int, stat func(int) (procStat, bool), reap func(int), now time.Time) {
	still := map[zombieKey]bool{}
	for _, pid := range children(self) {
		if tracked(pid) {
			continue
		}
		st, ok := stat(pid)
		if !ok || st.state != 'Z' {
			continue
		}
		k := zombieKey{pid, st.start}
		first, seen := r.seen[k]
		switch {
		case !seen:
			r.seen[k] = now
			still[k] = true
		case now.Sub(first) >= r.grace:
			reap(pid)
			delete(r.seen, k)
		default:
			still[k] = true
		}
	}
	for k := range r.seen {
		if !still[k] {
			delete(r.seen, k)
		}
	}
}
