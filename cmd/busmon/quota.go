package main

import (
	"fmt"
	"sort"
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

// quotaSeg shows both account windows where there is room. Wide uses one
// compact bar for short windows and a 10-cell bar for longer ones; medium
// shows percentages only; narrow keeps the tightest window.
func quotaSeg(provider string, b bus.BudgetSnapshot, holdUntil time.Time, isHeld bool, pace map[string]string, now time.Time, tier Tier, bars barView) Line {
	if b.Unlimited {
		return nil
	}
	name := Preview(providerLabel(provider))
	pr := providerRole(provider, RoleMuted) // the provider's own colour on its label, never on the fill
	if b.Unknown {
		l := Line{{Text: name, Role: pr}, {Text: " ?", Role: RoleMuted}}
		if isHeld {
			l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleError})
		}
		return l
	}
	windows := orderedQuotaWindows(b.Windows, now)
	if tier == TierNarrow {
		w, ok := tightestWindow(freshWindows(b, now), now)
		if !ok {
			w, ok = tightestWindow(windows, now)
		}
		if !ok {
			l := Line{{Text: name, Role: pr}, {Text: " ?", Role: RoleMuted}}
			if isHeld {
				l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleError})
			}
			return l
		}
		windows = []bus.BudgetWindow{w}
	}
	if len(windows) == 0 {
		l := Line{{Text: name, Role: pr}, {Text: " ?", Role: RoleMuted}}
		if isHeld {
			l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleError})
		}
		return l
	}
	l := Line{{Text: name, Role: pr}, {Text: " ", Role: RoleMuted}}
	matchedHold := false
	for i, w := range windows {
		if i > 0 {
			l = append(l, Seg{Text: " " + glyphVertical + " ", Role: RoleMuted})
		}
		aged := windowAged(w, b, now)
		holdsWindow := isHeld && w.ResetsAt == holdUntil.Unix()
		matchedHold = matchedHold || holdsWindow
		role := budgetRole(w.UsedPct)
		if aged {
			role = RoleMuted
		}
		if holdsWindow {
			role = RoleError
		}
		l = append(l, Seg{Text: Preview(windowLabel(w)) + " ", Role: RoleMuted})
		if tier == TierWide {
			cells := quotaBarCells(w)
			l = append(l, bars.gauge(barKey(provider, w.Name), w.UsedPct, cells, role)...)
			l = append(l, Seg{Text: " "})
		}
		l = append(l, Seg{Text: fmt.Sprintf("%.0f%%", w.UsedPct), Role: role})
		if aged {
			l = append(l, Seg{Text: " ?", Role: RoleMuted})
		}
		if holdsWindow {
			l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleError})
		} else if !aged && pace[quotaWindowKey(provider, w)] != "" {
			l = append(l, Seg{Text: " " + pace[quotaWindowKey(provider, w)], Role: RoleMuted})
		}
	}
	if isHeld && !matchedHold {
		l = append(l, Seg{Text: " held " + holdUntil.Local().Format("15:04"), Role: RoleError})
	}
	return l
}

func windowLabel(w bus.BudgetWindow) string {
	switch w.Name {
	case "weekly":
		return "7d"
	case "session":
		return "5h"
	default:
		return w.Name
	}
}

func quotaBarCells(w bus.BudgetWindow) int {
	minutes := w.Minutes
	if minutes == 0 {
		switch windowLabel(w) {
		case "5h":
			minutes = 300
		case "7d", "7d-opus", "7d-sonnet", "7d-overage":
			minutes = 7 * 24 * 60
		}
	}
	if minutes > 0 && minutes <= 6*60 {
		return 5
	}
	return 10
}

func orderedQuotaWindows(ws []bus.BudgetWindow, now time.Time) []bus.BudgetWindow {
	out := make([]bus.BudgetWindow, 0, len(ws))
	for _, w := range ws {
		if time.Unix(w.ResetsAt, 0).After(now) {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Minutes > 0 && b.Minutes > 0 && a.Minutes != b.Minutes {
			return a.Minutes < b.Minutes
		}
		if (a.Minutes > 0) != (b.Minutes > 0) {
			return a.Minutes > 0
		}
		if a.ResetsAt != b.ResetsAt {
			return a.ResetsAt < b.ResetsAt
		}
		return windowLabel(a) < windowLabel(b)
	})
	return out
}

func quotaWindowKey(provider string, w bus.BudgetWindow) string {
	return provider + "|" + w.Name + "|" + strconv.FormatInt(w.ResetsAt, 10)
}

// paceTracker remembers, per provider window (and its reset), the first
// reading busmon saw, to estimate the time left at the current pace. Display
// only: the dispatcher never acts on it.
type paceTracker struct {
	first map[string]paceSample
	last  map[string]string // provider|window|reset → "~25m"
}

type paceSample struct {
	pct float64
	at  time.Time
}

func (p *paceTracker) observe(budgets map[string]bus.BudgetSnapshot, now time.Time) {
	if p.first == nil {
		p.first, p.last = map[string]paceSample{}, map[string]string{}
	}
	active := map[string]bool{}
	for prov, b := range budgets {
		if b.Unlimited || b.Unknown {
			continue
		}
		for _, w := range orderedQuotaWindows(b.Windows, now) {
			key := quotaWindowKey(prov, w)
			if windowAged(w, b, now) {
				delete(p.first, key)
				delete(p.last, key)
				continue
			}
			active[key] = true
			f, seen := p.first[key]
			if !seen || w.UsedPct < f.pct {
				p.first[key] = paceSample{w.UsedPct, now}
				delete(p.last, key)
				continue
			}
			dt := now.Sub(f.at)
			if dt < 5*time.Minute || w.UsedPct <= f.pct {
				delete(p.last, key)
				continue
			}
			rate := (w.UsedPct - f.pct) / dt.Minutes() // % per minute
			left := time.Duration((100-w.UsedPct)/rate) * time.Minute
			if left > 0 && time.Unix(w.ResetsAt, 0).After(now.Add(left)) {
				p.last[key] = "~" + compactDur(left)
			} else {
				delete(p.last, key)
			}
		}
	}
	for key := range p.first {
		if !active[key] {
			delete(p.first, key)
			delete(p.last, key)
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

// quotaDetail is the account-scope quota row in an agent's detail. Windows
// are shared by every agent using this provider's account.
func quotaDetail(provider string, b bus.BudgetSnapshot, has bool, holdUntil time.Time, isHeld bool, now time.Time) []Line {
	var bd body
	bd.text("\n").seg(RoleAccent, "Quota").text("\n")
	if provider == "" {
		bd.seg(RoleMuted, "Account  provider not recorded (launched before v0.13): never held").text("\n")
		return bd.Lines()
	}
	label := Preview(providerLabel(provider))
	switch {
	case !has:
		bd.seg(RoleMuted, "Account  "+label+": no reading published (agentbus quota)").text("\n")
		return bd.Lines()
	case b.Unlimited:
		bd.seg(RoleMuted, "Account  "+label+": unlimited (shared by every agent using this provider)").text("\n")
		return bd.Lines()
	case b.Unknown || len(b.Windows) == 0:
		if isHeld {
			bd.seg(RoleMuted, "Account  "+label+": reading unknown now (shared by every agent using this provider)").text("\n")
			bd.seg(RoleAttention, "still held by the dispatcher until "+holdUntil.Local().Format("15:04")+" (the reset it recorded): not woken").text("\n")
		} else {
			bd.seg(RoleMuted, "Account  "+label+": unknown - never holds (shared by every agent using this provider)").text("\n")
		}
		return bd.Lines()
	}
	windows := append([]bus.BudgetWindow(nil), b.Windows...)
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].Minutes > 0 && windows[j].Minutes > 0 && windows[i].Minutes != windows[j].Minutes {
			return windows[i].Minutes < windows[j].Minutes
		}
		if windows[i].ResetsAt != windows[j].ResetsAt {
			return windows[i].ResetsAt < windows[j].ResetsAt
		}
		return windowLabel(windows[i]) < windowLabel(windows[j])
	})
	var parts []string
	matchedHold := false
	for _, w := range windows {
		reset := "reset past"
		if d := time.Unix(w.ResetsAt, 0).Sub(now); d > 0 {
			reset = "resets " + time.Unix(w.ResetsAt, 0).Local().Format("Mon 15:04") + " (in " + compactDur(d) + ")"
		}
		part := fmt.Sprintf("%s %.0f%% (%s)", windowLabel(w), w.UsedPct, reset)
		if windowAged(w, b, now) {
			part += " ?"
		}
		if isHeld && w.ResetsAt == holdUntil.Unix() {
			part += " held"
			matchedHold = true
		}
		parts = append(parts, part)
	}
	account := "Account  " + label + ": " + strings.Join(parts, " "+GlyphNone+" ") + " (shared by every agent using this provider)"
	bd.seg(RoleText, account).text("\n")
	if b.Source != "" {
		bd.seg(RoleMuted, "source: "+Preview(b.Source)).text("\n")
	}
	if isHeld && !matchedHold {
		bd.seg(RoleAttention, "held by the dispatcher until "+holdUntil.Local().Format("15:04")+": not woken").text("\n")
	} else if isHeld {
		bd.seg(RoleAttention, "held by the dispatcher until "+holdUntil.Local().Format("15:04")+": not woken").text("\n")
	}
	return bd.Lines()
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
