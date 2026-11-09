package main

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// backgroundReader reads agents' background work (progress-line §4): the
// bus has it; a reader without it shows none.
type backgroundReader interface {
	Backgrounds(ctx context.Context, agents []string) (map[string]bus.Background, error)
}

// backgroundText is an agent's background work as its row shows it:
// "… 2 checks running 4m"; "… ?" when its latest read failed; "" when it
// has none.
func backgroundText(a *agentState, now time.Time) string {
	if a != nil && a.bgFailed {
		return GlyphEllipsis + " ?"
	}
	if a == nil || a.bg == nil {
		return ""
	}
	t := GlyphEllipsis + " " + Preview(a.bg.Text)
	if !a.bg.Since.IsZero() {
		t += " " + humanAge(now.Sub(a.bg.Since))
	}
	return t
}

// backgroundChip is the header's chip: "… master: 2 checks running +1", the
// pilot's work first, then by name; "" when no agent has any.
func backgroundChip(agents map[string]*agentState, pilot string) string {
	var names []string
	for n, a := range agents {
		if a != nil && a.bg != nil {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == pilot) != (names[j] == pilot) {
			return names[i] == pilot
		}
		return names[i] < names[j]
	})
	chip := GlyphEllipsis + " " + names[0] + ": " + agents[names[0]].bg.Text
	if len(names) > 1 {
		chip += " +" + strconv.Itoa(len(names)-1)
	}
	return chip
}
