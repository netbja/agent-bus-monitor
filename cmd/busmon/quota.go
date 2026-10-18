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
// 03:10", "grok wk 59% ?". nil for an unlimited provider. With cells > 0 a bar
// of that many cells sits before the percentage ("claude 5h ████████░░ 82%").
func quotaSeg(provider string, b bus.BudgetSnapshot, holdUntil time.Time, isHeld bool, pace string, now time.Time, cells int) Line {
	if b.Unlimited {
		return nil
	}
	name := Preview(providerLabel(provider))
	w, ok := tightestWindow(freshWindows(b, now), now)
	if !ok {
		w, ok = tightestWindow(b.Windows, now) // all aged: show the tightest, marked ?
	}
	if b.Unknown || !ok {
		l := Line{{Text: name + " ?", Role: RoleMuted}}
		if isHeld {
			l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleAttention})
		}
		return l
	}
	win := w.Name
	if win == "weekly" {
		win = "wk"
	}
	aged := windowAged(w, b, now)
	role := budgetRole(w.UsedPct)
	if aged {
		role = RoleMuted
	}
	l := Line{{Text: name + " " + Preview(win) + " ", Role: RoleMuted}}
	if cells > 0 {
		n := clamp(int(w.UsedPct*float64(cells)/100+0.5), 0, cells)
		l = append(l, Seg{Text: strings.Repeat(GlyphGauge, n), Role: role},
			Seg{Text: strings.Repeat(GlyphBarEmpty, cells-n) + " ", Role: RoleDim})
	}
	l = append(l, Seg{Text: fmt.Sprintf("%.0f%%", w.UsedPct), Role: role})
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

// windowAged: the window's own observation (else the reading's) is missing or
// older than quotaMaxAge.
func windowAged(w bus.BudgetWindow, b bus.BudgetSnapshot, now time.Time) bool {
	obs := w.ObservedAt
	if obs == 0 {
		obs = b.ObservedAt
	}
	return obs == 0 || now.Sub(time.UnixMilli(obs)) > quotaMaxAge
}

// quotaDetail is the Quota section of an agent's detail (spec §8): its
// provider, every window with its reset, the source and the age of the
// reading, and whether the dispatcher holds it.
func quotaDetail(provider string, b bus.BudgetSnapshot, has bool, holdUntil time.Time, isHeld bool, now time.Time) string {
	var sb strings.Builder
	sb.WriteString("\n" + tg(RoleAccent, "Quota") + "\n")
	if provider == "" {
		sb.WriteString(tg(RoleMuted, "provider not recorded (launched before v0.13): never held") + "\n")
		return sb.String()
	}
	label := Preview(providerLabel(provider))
	switch {
	case !has:
		sb.WriteString(tg(RoleMuted, label+": no reading published (agentbus quota)") + "\n")
		return sb.String()
	case b.Unlimited:
		sb.WriteString(tg(RoleMuted, label+": unlimited") + "\n")
		return sb.String()
	case b.Unknown || len(b.Windows) == 0:
		if isHeld {
			sb.WriteString(tg(RoleMuted, label+": reading unknown now") + "\n")
			sb.WriteString(tg(RoleAttention, "still held by the dispatcher until "+holdUntil.Local().Format("15:04")+" (the reset it recorded): not woken") + "\n")
		} else {
			sb.WriteString(tg(RoleMuted, label+": unknown - never holds") + "\n")
		}
		return sb.String()
	}
	for _, w := range b.Windows {
		reset := "reset past"
		if d := time.Unix(w.ResetsAt, 0).Sub(now); d > 0 {
			reset = "resets " + time.Unix(w.ResetsAt, 0).Local().Format("Mon 15:04") + " (in " + compactDur(d) + ")"
		}
		age := "age unknown"
		obs := w.ObservedAt
		if obs == 0 {
			obs = b.ObservedAt
		}
		if obs > 0 {
			age = "observed " + humanAge(now.Sub(time.UnixMilli(obs))) + " ago"
		}
		line := fmt.Sprintf("%s %s %.0f%% %s %s %s %s", label, Preview(w.Name), w.UsedPct, GlyphNone, reset, GlyphNone, age)
		role := RoleText
		if windowAged(w, b, now) {
			role = RoleMuted
		}
		sb.WriteString(tg(role, line) + "\n")
	}
	if b.Source != "" {
		sb.WriteString(tg(RoleMuted, "source: "+Preview(b.Source)) + "\n")
	}
	if isHeld {
		sb.WriteString(tg(RoleAttention, "held by the dispatcher until "+holdUntil.Local().Format("15:04")+": not woken") + "\n")
	}
	return sb.String()
}

// freshWindows: the windows observed within quotaMaxAge.
func freshWindows(b bus.BudgetSnapshot, now time.Time) []bus.BudgetWindow {
	var out []bus.BudgetWindow
	for _, w := range b.Windows {
		if !windowAged(w, b, now) {
			out = append(out, w)
		}
	}
	return out
}
