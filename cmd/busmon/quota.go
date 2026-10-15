package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Quota in busmon (v0.13, spec §8): one gauge per provider in the header, its
// tightest window first, the pace as display only, and "held" on the agents of
// a provider the dispatcher holds.

// quotaMaxAge: older readings are shown with "?" (the policy ignores them).
const quotaMaxAge = 30 * time.Minute

// providerLabels name providers the way people call the tool.
var providerLabels = map[string]string{"anthropic": "claude", "openai": "codex", "xai": "grok", "moonshot": "kimi", "google": "gemini"}

func providerLabel(p string) string {
	if l, ok := providerLabels[p]; ok {
		return l
	}
	return p
}

// quotaHolds turns {p}:quota marks into provider → hold reset.
func quotaHolds(marks map[string]string) map[string]int64 {
	out := map[string]int64{}
	for k, v := range marks {
		if p, ok := strings.CutPrefix(k, "hold:"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[p] = n
			}
		}
	}
	return out
}

// held: the agent's provider is held by the dispatcher right now.
func held(holds map[string]int64, provider string, now time.Time) (time.Time, bool) {
	r, ok := holds[provider]
	if !ok || provider == "" || !time.Unix(r, 0).After(now) {
		return time.Time{}, false
	}
	return time.Unix(r, 0), true
}

// tightestWindow is the window closest to its limit among those whose reset
// is ahead; ok is false when none.
func tightestWindow(ws []bus.BudgetWindow, now time.Time) (bus.BudgetWindow, bool) {
	var best bus.BudgetWindow
	ok := false
	for _, w := range ws {
		if !time.Unix(w.ResetsAt, 0).After(now) {
			continue
		}
		if !ok || w.UsedPct > best.UsedPct {
			best, ok = w, true
		}
	}
	return best, ok
}

// quotaSeg is one provider's gauge: "claude 5h 82% ~25m", "codex 5h 96% held
// 03:10", "grok wk 59% ?". nil for an unlimited provider.
func quotaSeg(provider string, b bus.BudgetSnapshot, holdUntil time.Time, isHeld bool, pace string, now time.Time) Line {
	if b.Unlimited {
		return nil
	}
	name := Preview(providerLabel(provider))
	w, ok := tightestWindow(b.Windows, now)
	if b.Unknown || !ok {
		return Line{{Text: name + " ?", Role: RoleMuted}}
	}
	win := w.Name
	if win == "weekly" {
		win = "wk"
	}
	aged := b.ObservedAt == 0 || now.Sub(time.UnixMilli(b.ObservedAt)) > quotaMaxAge
	role := budgetRole(w.UsedPct)
	if aged {
		role = RoleMuted
	}
	l := Line{{Text: name + " " + Preview(win) + " ", Role: RoleMuted}, {Text: fmt.Sprintf("%.0f%%", w.UsedPct), Role: role}}
	switch {
	case isHeld:
		l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleAttention})
	case aged:
		l = append(l, Seg{Text: " ?", Role: RoleMuted})
	case pace != "":
		l = append(l, Seg{Text: " " + pace, Role: RoleMuted})
	}
	return l
}

// paceTracker remembers, per provider window (and its reset), the first
// reading busmon saw, to estimate the time left at the current pace. Display
// only: the dispatcher never acts on it.
type paceTracker struct {
	first map[string]paceSample
	last  map[string]string // provider → "~25m"
}

type paceSample struct {
	pct float64
	at  time.Time
}

func (p *paceTracker) observe(budgets map[string]bus.BudgetSnapshot, now time.Time) {
	if p.first == nil {
		p.first, p.last = map[string]paceSample{}, map[string]string{}
	}
	for prov, b := range budgets {
		w, ok := tightestWindow(b.Windows, now)
		if !ok || b.Unknown {
			delete(p.last, prov)
			continue
		}
		key := prov + "|" + w.Name + "|" + strconv.FormatInt(w.ResetsAt, 10)
		f, seen := p.first[key]
		if !seen || w.UsedPct < f.pct {
			p.first[key] = paceSample{w.UsedPct, now}
			delete(p.last, prov)
			continue
		}
		dt := now.Sub(f.at)
		if dt < 5*time.Minute || w.UsedPct <= f.pct {
			delete(p.last, prov)
			continue
		}
		rate := (w.UsedPct - f.pct) / dt.Minutes() // % per minute
		left := time.Duration((100-w.UsedPct)/rate) * time.Minute
		if left > 0 && time.Unix(w.ResetsAt, 0).After(now.Add(left)) {
			p.last[prov] = "~" + compactDur(left)
		} else {
			delete(p.last, prov)
		}
	}
}
