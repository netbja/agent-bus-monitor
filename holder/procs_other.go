//go:build !linux

package holder

// processTree needs /proc: none on this platform yet (spec 4.3b, macOS
// through sysctl later).
func processTree(int) []ProcInfo { return nil }

func childrenOf(int) []int { return nil }

// No /proc: the group is not scanned, and the leader is reaped at once (its
// group's leftovers are not ended at its exit there).
func othersInGroup(int) bool { return false }

func waitExited(int) {}
