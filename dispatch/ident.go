package dispatch

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// statFields is what process identity needs from /proc/<pid>/stat.
type statFields struct {
	ppid  int
	start uint64 // field 22: start time in clock ticks since boot
}

// parseStat reads one /proc/<pid>/stat line. The comm field (2) is in
// parentheses and may itself hold spaces or parens, so fields are counted from
// the LAST ')' — field 3 is the first one after it.
func parseStat(line string) (statFields, error) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return statFields{}, fmt.Errorf("stat: no comm field")
	}
	f := strings.Fields(line[i+1:]) // f[0] is field 3
	if len(f) < 20 {
		return statFields{}, fmt.Errorf("stat: %d fields after comm", len(f))
	}
	ppid, err := strconv.Atoi(f[4-3])
	if err != nil {
		return statFields{}, fmt.Errorf("stat ppid: %w", err)
	}
	start, err := strconv.ParseUint(f[22-3], 10, 64)
	if err != nil {
		return statFields{}, fmt.Errorf("stat starttime: %w", err)
	}
	return statFields{ppid: ppid, start: start}, nil
}

func readStat(pid int) (statFields, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return statFields{}, err
	}
	return parseStat(string(b))
}

// ProcStart is pid's start time (field 22 of /proc/<pid>/stat). With the pid it
// identifies one process for its whole life: a recycled pid gets a new value.
func ProcStart(pid int) (uint64, error) {
	f, err := readStat(pid)
	if err != nil {
		return 0, err
	}
	return f.start, nil
}

// AncestorAmong walks the ppid chain from self (self included) and returns the
// first process found among candidates.
func AncestorAmong(candidates []int, self int) (int, bool) {
	if len(candidates) == 0 {
		return 0, false
	}
	want := make(map[int]bool, len(candidates))
	for _, c := range candidates {
		want[c] = true
	}
	for pid, hops := self, 0; pid > 0 && hops < 256; hops++ {
		if want[pid] {
			return pid, true
		}
		if pid == 1 {
			break
		}
		f, err := readStat(pid)
		if err != nil {
			break
		}
		pid = f.ppid
	}
	return 0, false
}
