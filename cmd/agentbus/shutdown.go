package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// shutdownBlockers lists why a global shutdown must be refused: board tasks
// that are not done, and peers still working or blocked. self (the agent
// running the shutdown, usually master) is excluded — it is working by
// definition. A peer whose last heartbeat is older than agentStaleAfter is
// dead, not busy, and does not block. Sorted for stable output.
func shutdownBlockers(board map[string]bus.BoardEntry, agents map[string]bus.AgentSnapshot, self string, now time.Time) []string {
	var out []string
	for task, e := range board {
		if e.State != "done" {
			out = append(out, fmt.Sprintf("task %s is %s (%s)", task, e.State, e.Owner))
		}
	}
	for name, a := range agents {
		if name == self {
			continue
		}
		if a.State != "working" && a.State != "blocked" {
			continue
		}
		if now.Sub(time.UnixMilli(a.TS)) > agentStaleAfter {
			continue // stale heartbeat: dead, not busy
		}
		out = append(out, fmt.Sprintf("agent %s is %s", name, a.State))
	}
	sort.Strings(out)
	return out
}
