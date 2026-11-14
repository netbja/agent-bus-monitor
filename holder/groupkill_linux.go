//go:build linux

package holder

import (
	"errors"
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

// nowTicks is the time since boot in /proc's clock ticks (USER_HZ, 100).
func nowTicks() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	if err != nil || secs < 0 {
		return 0
	}
	return uint64(secs*100) + 1 // rounded up: a process started this tick counts
}

func killGroupMembers(pgid int, from, to uint64) (int, error) {
	if pgid <= 1 || from == 0 || to < from {
		return 0, nil
	}
	if e, err := readEntry(pgid); err == nil && e.start != from {
		return 0, nil // the leader's pid is another process's now
	}
	killed := map[int]bool{}
	for pass := 0; pass < 5; pass++ {
		n, err := killPass(pgid, from, to, killed)
		if err != nil {
			return len(killed), err
		}
		if n == 0 {
			break
		}
	}
	return len(killed), nil
}

// killPass scans /proc once and kills the members it finds.
func killPass(pgid int, from, to uint64, killed map[int]bool) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range entries {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || killed[pid] {
			continue
		}
		e, err := readEntry(pid)
		if err != nil || e.pgrp != pgid || e.start < from || e.start > to {
			continue
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			continue // gone
		}
		// The pin may have caught a process that took the pid after the scan:
		// the same start time proves it is the one scanned.
		if again, err := readEntry(pid); err == nil && again == e {
			if unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0) == nil {
				killed[pid] = true
				n++
			}
		}
		unix.Close(fd)
	}
	return n, nil
}
