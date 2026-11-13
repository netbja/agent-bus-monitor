//go:build !linux

package holder

// processTree needs /proc: none on this platform yet (spec 4.3b, macOS
// through sysctl later).
func processTree(int) []ProcInfo { return nil }

func childrenOf(int) []int { return nil }
