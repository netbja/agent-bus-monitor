//go:build !linux

package holder

import "errors"

// processTree needs /proc: none on this platform yet (spec 4.3b, macOS
// through sysctl later).
func processTree(int) []ProcInfo { return nil }

func childrenOf(int) []int { return nil }

// No /proc: the group is not scanned, and the leader is reaped at once (its
// group's leftovers are not ended at its exit there).
func othersInGroup(int) bool { return false }

func waitExited(int) {}

// leaderHeld: no WNOWAIT wait here, the leader may be reaped already: its
// group is never signalled at its exit.
const leaderHeld = false

// procStartOf needs /proc: without it a supervisor cannot be told from a
// reused pid, so nothing is judged (Supervised's ProcStart replaces it).
func procStartOf(int) (uint64, error) {
	return 0, errors.New("no process start time on this platform without a reader (ProcStart)")
}
