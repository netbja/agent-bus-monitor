package holder

import (
	"slices"
	"testing"
)

// One sweep: an exited orphan is reaped; a terminal's own program, exited
// or not, is left to its Wait (spec 4.4: never wait(-1)); a running orphan
// waits for its exit.
func TestReapSweep(t *testing.T) {
	states := map[int]byte{10: 'Z', 11: 'Z', 12: 'S', 13: 'S'}
	tracked := map[int]bool{11: true, 13: true}
	var reaped []int
	reapSweep(1, func(pid int) bool { return tracked[pid] },
		func(int) []int { return []int{10, 11, 12, 13, 14} },
		func(pid int) (procStat, bool) { s, ok := states[pid]; return procStat{pid: pid, state: s}, ok },
		func(pid int) { reaped = append(reaped, pid) })
	if !slices.Equal(reaped, []int{10}) {
		t.Fatalf("reaped %v, want only the exited orphan 10", reaped)
	}
}
