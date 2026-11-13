package holder

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// clockTicks is USER_HZ, the unit of /proc/<pid>/stat's times (100 on
// every Linux the project runs on).
const clockTicks = 100

type procStat struct {
	pid, ppid, pgrp int
	comm            string
	state           byte
	start           uint64
	cpuTicks        int64
}

// readProcStat reads /proc/<pid>/stat: its comm in parentheses may hold
// spaces, so the fields are read after its last ')'.
func readProcStat(pid int) (procStat, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	s := string(b)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < l {
		return procStat{}, false
	}
	f := strings.Fields(s[r+1:])
	if len(f) < 20 {
		return procStat{}, false
	}
	ppid, _ := strconv.Atoi(f[1])
	pgrp, _ := strconv.Atoi(f[2])
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	st, _ := strconv.ParseInt(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	return procStat{pid: pid, ppid: ppid, pgrp: pgrp, comm: s[l+1 : r], state: f[0][0], start: start, cpuTicks: ut + st}, true
}

// processTree is root and every descendant of it, root first, read from
// /proc (spec 4.3b: the terminal's process tree).
func processTree(root int) []ProcInfo {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	kids := map[int][]procStat{}
	var top procStat
	found := false
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		st, ok := readProcStat(pid)
		if !ok {
			continue
		}
		if pid == root {
			top, found = st, true
		}
		kids[st.ppid] = append(kids[st.ppid], st)
	}
	if !found {
		return nil
	}
	var out []ProcInfo
	queue := []procStat{top}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		out = append(out, ProcInfo{PID: p.pid, PPID: p.ppid, Start: p.start, Command: p.comm, CPUms: p.cpuTicks * 1000 / clockTicks})
		queue = append(queue, kids[p.pid]...)
	}
	return out
}

// childrenOf is pid's children as the kernel lists them
// (/proc/<pid>/task/*/children), for the subreaper.
func childrenOf(pid int) []int {
	tasks, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
	if err != nil {
		return nil
	}
	var out []int
	for _, t := range tasks {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + t.Name() + "/children")
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(b)) {
			if c, err := strconv.Atoi(f); err == nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// othersInGroup: a process of the group pgid, other than its leader
// (pgid itself) and not a zombie, still runs.
func othersInGroup(pgid int) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == pgid {
			continue
		}
		if st, ok := readProcStat(pid); ok && st.pgrp == pgid && st.state != 'Z' {
			return true
		}
	}
	return false
}

// leaderHeld: waitExited leaves the leader unreaped (WNOWAIT), so its group's
// number is still the terminal's while endGroup signals it.
const leaderHeld = true

// waitExited returns once pid exited, leaving it unreaped (WNOWAIT): its
// own Wait reaps it after its group was ended.
func waitExited(pid int) {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return
		}
	}
}
