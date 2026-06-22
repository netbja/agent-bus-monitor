package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// usageTable renders each agent's latest usage, one aged line each, sorted by
// name. Empty fields are omitted (joined with " · ").
func usageTable(m map[string]bus.UsageSnapshot, now time.Time) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		parts := make([]string, 0, 5)
		for _, f := range []string{s.Model, s.Ctx, s.Weekly, s.Session, s.Reset} {
			if f != "" {
				parts = append(parts, f)
			}
		}
		age := ""
		if s.TS != 0 {
			age = humanAge(now.Sub(time.UnixMilli(s.TS)))
		}
		fmt.Fprintf(&sb, "%-12s %-44s %s\n", n, strings.Join(parts, " · "), age)
	}
	return sb.String()
}
