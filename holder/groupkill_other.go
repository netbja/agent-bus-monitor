//go:build !linux

package holder

// Without /proc there is no start time: the holder records no group, and
// nothing is killed.
func killGroupMembers(pgid int, from, to uint64) (int, error) { return 0, nil }

// NowTicks is 0: no start time without /proc.
func NowTicks() uint64 { return 0 }
