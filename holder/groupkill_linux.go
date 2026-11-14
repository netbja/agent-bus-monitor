//go:build linux

package holder

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// A dead holder's terminal groups are killed process by process, never by
// signalling the group number (reviewer-codex): once a group has emptied,
// its number may be handed to an unrelated new group. A process is the
// recorded group's when its group is pgid and it started between the
// recorded leader (from) and the moment the holder's death was seen (to):
// a group made after the old one emptied and its number was reused started
// after that moment. Each one is pinned by a pidfd, its identity read again
// through /proc after the pin, and signalled through the pidfd, so a pid
// reused between the scan and the kill is never hit. If a live process holds
// the leader's pid with another start time, the number is another's now and
// nothing is touched. A member forked during the scan is found by the next
// pass.

// The pidfd calls (tests make them fail).
var (
	pidfdOpen = unix.PidfdOpen
	pidfdSend = unix.PidfdSendSignal
)

// procEntry is what the kill needs of /proc/<pid>/stat.
type procEntry struct {
	pgrp  int
	start uint64
}

// readEntry parses /proc/<pid>/stat: pgrp is field 5, starttime field 22,
// counted from the last ')' (the comm field may hold spaces and parens).
func readEntry(pid int) (procEntry, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procEntry{}, err
	}
	line := string(b)
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return procEntry{}, errors.New("stat: no comm field")
	}
	f := strings.Fields(line[i+1:]) // f[0] is field 3
	if len(f) < 20 {
		return procEntry{}, errors.New("stat: short")
	}
	pgrp, err := strconv.Atoi(f[5-3])
	if err != nil {
		return procEntry{}, err
	}
	start, err := strconv.ParseUint(f[22-3], 10, 64)
	if err != nil {
		return procEntry{}, err
	}
	return procEntry{pgrp: pgrp, start: start}, nil
}

// NowTicks is the time since boot in /proc's clock ticks (USER_HZ, 100): the
// cutoff KillRecordedGroups takes, once, when the holder's exit is seen. It
// is the last tick that is surely over: the one being read is uncertain (a
// group made after the capture may start in it), so it is excluded
// (reviewer-codex). The price: a survivor that started in the last tick or
// two before the holder died (10-20 ms) is not killed.
func NowTicks() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	if err != nil || secs < 0 {
		return 0
	}
	t := uint64(secs * 100) // the tick being read
	if t < 2 {
		return 0
	}
	return t - 1
}

func killGroupMembers(pgid int, from, to uint64) (int, error) {
	if pgid <= 1 || from == 0 || to < from {
		return 0, nil
	}
	if e, err := readEntry(pgid); err == nil && e.start != from {
		return 0, nil // the leader's pid is another process's now
	}
	killed, refused := map[int]bool{}, map[int]error{}
	for pass := 0; pass < 5; pass++ {
		n, err := killPass(pgid, from, to, killed, refused)
		if err != nil {
			return len(killed), err // no pidfd at all: every member would fail alike
		}
		if n == 0 {
			break
		}
	}
	errs := make([]error, 0, len(refused))
	for _, err := range refused {
		errs = append(errs, err)
	}
	return len(killed), errors.Join(errs...)
}

// killPass scans /proc once and kills the members it finds. A member that
// refuses (EPERM: a root child of sudo) is recorded in refused and skipped,
// never an end to the scan: the others still die (foureyes). Only ENOSYS (no
// pidfd on this kernel) ends it.
func killPass(pgid int, from, to uint64, killed map[int]bool, refused map[int]error) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range entries {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || killed[pid] || refused[pid] != nil {
			continue
		}
		e, err := readEntry(pid)
		if err != nil || e.pgrp != pgid || e.start < from || e.start > to {
			continue
		}
		fd, err := pidfdOpen(pid, 0)
		switch {
		case errors.Is(err, unix.ESRCH):
			continue // gone
		case errors.Is(err, unix.ENOSYS):
			return n, fmt.Errorf("holder: pidfd_open: %w (Linux 5.3 or later is needed)", err)
		case err != nil:
			// Refused: never a silent skip (reviewer-codex), never the end
			// of the scan (foureyes).
			refused[pid] = fmt.Errorf("holder: pidfd_open %d: %w", pid, err)
			continue
		}
		// The pin may have caught a process that took the pid after the scan:
		// the same start time proves it is the one scanned.
		if again, err := readEntry(pid); err == nil && again == e {
			err := pidfdSend(fd, unix.SIGKILL, nil, 0)
			switch {
			case err == nil:
				killed[pid] = true
				n++
			case errors.Is(err, unix.ENOSYS):
				unix.Close(fd)
				return n, fmt.Errorf("holder: pidfd_send_signal: %w (Linux 5.1 or later is needed)", err)
			case !errors.Is(err, unix.ESRCH):
				refused[pid] = fmt.Errorf("holder: pidfd_send_signal %d: %w", pid, err)
			}
		}
		unix.Close(fd)
	}
	return n, nil
}
