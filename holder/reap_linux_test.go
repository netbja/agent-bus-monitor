package holder

import (
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"
)

// Sweeps: an exited orphan is reaped once it has stayed a zombie for the
// grace, not at first sight (a command of this process is reaped by its own
// Wait within that time); a terminal's own program, exited or not, is left
// to its Wait (spec 4.4: never wait(-1)); a running orphan waits for its
// exit; a pid reused by a new process starts its grace again.
func TestReapSweep(t *testing.T) {
	states := map[int]procStat{10: {pid: 10, state: 'Z', start: 100}, 11: {pid: 11, state: 'Z', start: 100},
		12: {pid: 12, state: 'S', start: 100}, 13: {pid: 13, state: 'S', start: 100}}
	tracked := map[int]bool{11: true, 13: true}
	var reaped []int
	r := newOrphanReaper(2 * time.Second)
	sweep := func(at time.Time) {
		r.sweep(1, func(pid int) bool { return tracked[pid] },
			func(int) []int { return []int{10, 11, 12, 13, 14} },
			func(pid int) (procStat, bool) { s, ok := states[pid]; return s, ok },
			func(pid int) { reaped = append(reaped, pid) }, at)
	}
	t0 := time.Unix(1000, 0)
	sweep(t0)
	if len(reaped) != 0 {
		t.Fatalf("reaped %v at first sight", reaped)
	}
	sweep(t0.Add(time.Second))
	if len(reaped) != 0 {
		t.Fatalf("reaped %v within the grace", reaped)
	}
	sweep(t0.Add(2 * time.Second))
	if !slices.Equal(reaped, []int{10}) {
		t.Fatalf("reaped %v, want only the exited orphan 10", reaped)
	}
	// 10's pid reused by a new process that exits: a new grace.
	states[10] = procStat{pid: 10, state: 'Z', start: 200}
	reaped = nil
	sweep(t0.Add(3 * time.Second))
	if len(reaped) != 0 {
		t.Fatalf("a reused pid reaped at once: %v", reaped)
	}
	sweep(t0.Add(5 * time.Second))
	if !slices.Equal(reaped, []int{10}) {
		t.Fatalf("reaped %v, want the new 10 after its grace", reaped)
	}
	if len(r.seen) != 0 {
		t.Fatalf("seen keeps %v after the reaps", r.seen)
	}
}

// The waitid flake (release check, 2026-11-17): the holder's reaper ran in
// the same process as code that runs its own commands (os/exec), and reaped
// their exited children before their own Wait did, which then failed with
// "waitid: no child processes". With the reaper running, every command of
// this process must still be waited for by its own Wait.
func TestReaperLeavesThisProcessCommandsToTheirWait(t *testing.T) {
	startReaper()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				if err := exec.Command("true").Run(); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d command(s) lost to the reaper, first: %v", len(errs), errs[0])
	}
}
