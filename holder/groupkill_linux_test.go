//go:build linux

package holder

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/dispatch"

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
			time.Sleep(50 * time.Millisecond) // past the cutoff's excluded ticks
			if _, err := killGroupMembers(pg, start, NowTicks()); err == nil {
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
	time.Sleep(50 * time.Millisecond)
	if _, err := killGroupMembers(pg, start, NowTicks()); err != nil && !errors.Is(err, unix.ESRCH) {
		t.Fatalf("ESRCH: %v", err)
	} else if err != nil {
		t.Fatalf("ESRCH reported as an error: %v", err)
	}
}

// A member that refuses the signal (EPERM: a root child of sudo in the
// agent's group) does not save the others: the scan goes on, the others are
// killed, and the refusal is returned (foureyes).
func TestKillGroupMembersGoesOnPastARefusal(t *testing.T) {
	needProc(t)
	leader := exec.Command("/bin/sh", "-c", "trap '' HUP; sleep 300 & sleep 300 & echo $!; sleep 0.2; exit 0")
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	pg := leader.Process.Pid
	t.Cleanup(func() { syscall.Kill(-pg, syscall.SIGKILL) })
	start, err := dispatch.ProcStart(pg)
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Wait(); err != nil {
		t.Fatal(err)
	}
	send := pidfdSend
	t.Cleanup(func() { pidfdSend = send })
	refused := 0
	pidfdSend = func(fd int, sig unix.Signal, info *unix.Siginfo, flags int) error {
		if refused == 0 {
			refused++
			return unix.EPERM
		}
		return send(fd, sig, info, flags)
	}
	k, err := killGroupMembers(pg, start, NowTicks())
	if k != 1 || err == nil || !errors.Is(err, unix.EPERM) {
		t.Fatalf("killed %d, %v: want the other member killed and the refusal returned", k, err)
	}
	alive := 0
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		alive = 0
		for _, pid := range groupMembers(t, pg) {
			if st := procState(pid); st != "" && st != "Z" {
				alive++
			}
		}
		if alive == 1 {
			return
		}
	}
	t.Fatalf("%d members alive, want only the refused one", alive)
}

// groupMembers lists the processes of group pg (/proc).
func groupMembers(t *testing.T, pg int) []int {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, d := range ents {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		if e, err := readEntry(pid); err == nil && e.pgrp == pg {
			out = append(out, pid)
		}
	}
	return out
}

// The cutoff never reaches a process started after it was taken: the tick it
// was read in is uncertain and excluded (reviewer-codex). Repeated, since a
// start in the same tick is what the old rounding let through.
func TestNowTicksExcludesWhatStartsAfter(t *testing.T) {
	needProc(t)
	for i := 0; i < 50; i++ {
		cut := NowTicks()
		c := exec.Command("/bin/sleep", "5")
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		start, err := dispatch.ProcStart(c.Process.Pid)
		c.Process.Kill()
		c.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if start <= cut {
			t.Fatalf("run %d: a process started after the cutoff %d has start %d <= it", i, cut, start)
		}
	}
}
