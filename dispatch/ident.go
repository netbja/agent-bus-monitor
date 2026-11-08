package dispatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
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

// ErrNoProcess: /proc, readable on this system, lists no such pid. Only it
// proves a process gone; any other error (no /proc, as on macOS, or a
// failed read) proves nothing (pop-closed-by-hand, reviewer-codex).
var ErrNoProcess = errors.New("no such process")

// ProcStart is pid's start time (field 22 of /proc/<pid>/stat). With the pid it
// identifies one process for its whole life: a recycled pid gets a new value.
func ProcStart(pid int) (uint64, error) {
	f, err := readStat(pid)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if _, serr := os.Stat("/proc/self/stat"); serr == nil {
				return 0, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
			}
		}
		return 0, err
	}
	return f.start, nil
}

// AncestorAmong returns the OUTERMOST candidate on self's ancestor chain,
// self excluded. The pane's foreground set can include short-lived tool
// shells (or this very process) below the agent; the long-lived agent is the
// one furthest up, so the walk keeps the last match on its way to init.
func AncestorAmong(candidates []int, self int) (int, bool) {
	return ancestorAmong(candidates, self, func(pid int) (int, error) {
		f, err := readStat(pid)
		return f.ppid, err
	})
}

func ancestorAmong(candidates []int, self int, ppidOf func(int) (int, error)) (int, bool) {
	if len(candidates) == 0 {
		return 0, false
	}
	want := make(map[int]bool, len(candidates))
	for _, c := range candidates {
		want[c] = true
	}
	found, ok := 0, false
	pid, err := ppidOf(self)
	for hops := 0; err == nil && pid > 0 && hops < 256; hops++ {
		if want[pid] {
			found, ok = pid, true
		}
		if pid == 1 {
			break
		}
		pid, err = ppidOf(pid)
	}
	return found, ok
}

// ProcID is one process for its whole life: a pid and its start time.
type ProcID struct {
	PID   int
	Start uint64
}

// Ancestors is self's ancestor chain, nearest first, self excluded, each with
// its start time: up to init, or to the first process that cannot be read.
// Like ProcStart it reads /proc, so it is Linux only: elsewhere it is empty,
// and no agent has a verified identity to prove a session against anyway.
func Ancestors(self int) []ProcID {
	return ancestors(self, readStat)
}

func ancestors(self int, statOf func(int) (statFields, error)) []ProcID {
	var out []ProcID
	f, err := statOf(self)
	for hops := 0; err == nil && f.ppid > 0 && hops < 256; hops++ {
		pid := f.ppid
		if f, err = statOf(pid); err != nil {
			break
		}
		out = append(out, ProcID{PID: pid, Start: f.start})
	}
	return out
}

// procAgent is pid's "<project>/<agent>" from /proc/<pid>/environ ("" when
// unreadable, as on macOS, or not an agent's): a process of the same user,
// read only, never trusted for more than a restart in place.
func procAgent(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	return agentFromEnviron(b)
}

// agentFromEnviron reads AGENT_BUS_PROJECT and AGENT_BUS_AGENT from a
// NUL-separated environment: both, valid names, else "".
func agentFromEnviron(b []byte) string {
	var project, agent string
	for _, kv := range strings.Split(string(b), "\x00") {
		if v, ok := strings.CutPrefix(kv, "AGENT_BUS_PROJECT="); ok {
			project = v
		}
		if v, ok := strings.CutPrefix(kv, "AGENT_BUS_AGENT="); ok {
			agent = v
		}
	}
	if !bus.ValidName(project) || !bus.ValidName(agent) {
		return ""
	}
	return project + "/" + agent
}
