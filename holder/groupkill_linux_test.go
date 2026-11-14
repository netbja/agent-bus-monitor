//go:build linux

package holder

import (
	"errors"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A pidfd call that fails for another reason than a process gone (ESRCH) is
// an error, never a silent skip: a kernel without pidfd_open (ENOSYS), or a
// refused signal, must not pass for "nothing left to kill" (reviewer-codex).
func TestKillGroupMembersReportsAPidfdFailure(t *testing.T) {
	needProc(t)
	for name, set := range map[string]func(){
		"open ENOSYS": func() { pidfdOpen = func(int, int) (int, error) { return -1, unix.ENOSYS } },
		"send EPERM": func() {
			pidfdSend = func(int, unix.Signal, *unix.Siginfo, int) error { return unix.EPERM }
		},
	} {
		t.Run(name, func(t *testing.T) {
			open, send := pidfdOpen, pidfdSend
			t.Cleanup(func() { pidfdOpen, pidfdSend = open, send })
			set()
			_, start, member := groupLeader(t, "0", "0")
			pg, err := syscall.Getpgid(member)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := killGroupMembers(pg, start, nowTicks()); err == nil {
				t.Fatal("a pidfd failure passed for success")
			}
		})
	}
	// ESRCH is a process gone: not an error.
	open := pidfdOpen
	t.Cleanup(func() { pidfdOpen = open })
	pidfdOpen = func(int, int) (int, error) { return -1, unix.ESRCH }
	_, start, member := groupLeader(t, "0", "0")
	pg, _ := syscall.Getpgid(member)
	if _, err := killGroupMembers(pg, start, nowTicks()); err != nil && !errors.Is(err, unix.ESRCH) {
		t.Fatalf("ESRCH: %v", err)
	} else if err != nil {
		t.Fatalf("ESRCH reported as an error: %v", err)
	}
}
