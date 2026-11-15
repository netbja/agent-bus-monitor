//go:build !linux

package holder

import (
	"errors"
	"syscall"
)

// Without /proc there is no start time: the holder records no group, and
// nothing is killed.
func killGroupMembers(pgid int, from, to uint64) (int, error) { return 0, nil }

// NowTicks is 0: no start time without /proc.
func NowTicks() uint64 { return 0 }

// signalPinned: without a pidfd a signal cannot be pinned to one process.
func signalPinned(pid int, start uint64, sig syscall.Signal, startOf func(int) (uint64, error)) error {
	return errors.New("no pinned signal on this platform")
}
