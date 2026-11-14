//go:build !linux

package holder

// Without /proc there is no start time: the holder records no group, and
// nothing is killed.
func killGroupMembers(pgid int, from, to uint64) (int, error) { return 0, nil }

func nowTicks() uint64 { return 0 }
