package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/usage"
)

// budgetTable renders one line per provider: the account windows and how long
// until each resets, sorted by provider name.
func budgetTable(m map[string]bus.BudgetSnapshot, now time.Time) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		parts := []string{
			fmt.Sprintf("session %s", pctWindow(s.SessionPct, s.SessionReset, now)),
			fmt.Sprintf("weekly %s", pctWindow(s.WeeklyPct, s.WeeklyReset, now)),
		}
		for _, k := range sortedKeys(s.Extra) {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", strings.TrimSuffix(k, "_pct"), s.Extra[k]))
		}
		age := ""
		if s.TS != 0 {
			age = humanAge(now.Sub(time.UnixMilli(s.TS)))
		}
		fmt.Fprintf(&sb, "%-12s %-52s %s\n", n, strings.Join(parts, " · "), age)
	}
	return sb.String()
}

// pctWindow formats "44% (resets in 5d1h)". A reset already in the past is not
// shown: a window that has rolled over makes its old percentage meaningless, and
// printing "resets in -3h" reads as a bug rather than as staleness.
func pctWindow(pct float64, reset string, now time.Time) string {
	out := fmt.Sprintf("%.0f%%", pct)
	if reset == "" {
		return out
	}
	t, err := time.Parse(time.RFC3339, reset)
	if err != nil {
		return out
	}
	if d := t.Sub(now); d > 0 {
		return fmt.Sprintf("%s (resets in %s)", out, humanUntil(d))
	}
	return out + " (window rolled over)"
}

func humanUntil(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// collectBudgets reads every account budget source we know about. Errors are
// returned as notes, never as failures: refresh is a caretaker command, and a
// missing ccstatusline cache must not stop the per-agent half from publishing.
func collectBudgets(ccPath string) (map[string]bus.BudgetSnapshot, []string) {
	out := map[string]bus.BudgetSnapshot{}
	var notes []string
	if ccPath == "" {
		return out, []string{"no ccstatusline cache path (set HOME or XDG_CACHE_HOME)"}
	}
	snap, err := usage.ReadCCStatusline(ccPath)
	if err != nil {
		return out, []string{fmt.Sprintf("anthropic budget unavailable: %v", err)}
	}
	out[usage.AnthropicProvider] = snap
	return out, notes
}
