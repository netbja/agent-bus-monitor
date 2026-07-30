package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// boardTable renders the shared ownership board (task → owner/state/branch),
// newest activity first. An agent reads this before starting work — a task
// already owned by a peer is off-limits.
func boardTable(m map[string]bus.BoardEntry, now time.Time) string {
	if len(m) == 0 {
		return "(board empty)\n"
	}
	tasks := make([]string, 0, len(m))
	for t := range m {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if m[tasks[i]].Updated != m[tasks[j]].Updated {
			return m[tasks[i]].Updated > m[tasks[j]].Updated
		}
		return tasks[i] < tasks[j]
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-16s %-12s %-9s %-24s %s\n", "TASK", "OWNER", "STATE", "BRANCH", "AGE")
	for _, t := range tasks {
		e := m[t]
		fmt.Fprintf(&sb, "%-16s %-12s %-9s %-24s %s\n",
			t, e.Owner, e.State, e.Branch, humanAge(now.Sub(time.Unix(e.Updated, 0))))
	}
	return sb.String()
}
