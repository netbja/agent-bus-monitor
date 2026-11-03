package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The U page's Accounts zone as cards (usage-cards, the person's choice from
// master's mock-up): per provider a head line, one row per window with a
// full-width thin bar and its pace mark, and a pace sentence under it.

// cardMaxWidth is the widest a card is drawn (usage-card-width, the person's
// approval of the cards at 80 cells): on a wider page it stays at the left,
// the rest of the zone blank.
const cardMaxWidth = 80

// accountCard is one provider's card: its name upper case in its colour, at
// the right its busiest window (or its hold); one row per window; the pace
// sentence. Every line fits width.
func accountCard(icons, p string, b bus.BudgetSnapshot, holds map[string]int64, now time.Time, width int, bars barView) []Line {
	indent := Line{S(RoleText, "  ")}
	if icons != iconsOff && icons != "" {
		indent = append(indent, iconPair(providerIcon(icons, p), p))
	}
	inner := max(0, width-2-indent.Width())                                                                                               // a card keeps two blank cells at the right, as the rule does
	name := Seg{Text: Truncate(strings.ToUpper(Preview(providerLabel(p))), max(1, inner/2)), Role: providerRole(p, RoleText), Bold: true} // the header's name
	var right Seg
	until, isHeld := held(holds, p, now)
	if busy, ok := busiestWindow(b.Windows); isHeld {
		right = Seg{Text: "held until " + resetClock(until, now), Role: RoleError}
	} else if ok {
		right = Seg{Text: fmt.Sprintf("%s %s %.0f%%", windowLabel(busy), GlyphNone, busy.UsedPct), Role: budgetRole(busy.UsedPct)}
	}
	right.Text = Truncate(right.Text, max(0, inner-Width(name.Text)-1)) // Fit would pad it to all the room
	head := append(append(Line{}, indent...), name, S(RoleText, strings.Repeat(" ", max(1, inner-Width(name.Text)-Width(right.Text)))), right)
	out := []Line{head}

	// The rows' tails share one width, so the bars line up within the card.
	tails := make([]string, len(b.Windows))
	tailW := 0
	for i, w := range b.Windows {
		tails[i] = windowTail(w, b, now)
		tailW = max(tailW, Width(tails[i]))
	}
	barW := max(4, inner-4-1-5-1-tailW) // label, bar, " 100%", tail
	for i, w := range b.Windows {
		row := append(append(Line{}, indent...), S(RoleMuted, pad(Fit(Preview(accountWindowName(w.Name)), 3), 4)))
		if windowUnobserved(w) { // unknown, never a 0 % bar
			row = append(row, S(RoleMuted, "?"))
			out = append(out, FitLine(row, width))
			continue
		}
		row = append(row, bars.thin(barKey(p, w.Name), w.UsedPct, windowElapsedPct(w, now), barW, budgetRole(w.UsedPct))...)
		row = append(row, S(budgetRole(w.UsedPct), fmt.Sprintf(" %4.0f%%", w.UsedPct)), S(RoleMuted, " "+tails[i]))
		out = append(out, FitLine(row, width))
	}
	if s := paceSentence(b, now); s != "" {
		out = append(out, FitLine(append(append(Line{}, indent...), S(RoleMuted, s)), width))
	}
	return out
}

// accountWindowName is a window's name in the 3-cell column: a window named
// in words (xai's billing periods) gets a short label, so weekly reads wk,
// not a cut "we…"; any other name is shown as it is, fitted.
func accountWindowName(name string) string {
	switch name {
	case "weekly":
		return "wk"
	case "monthly":
		return "mo"
	case "daily":
		return "day"
	}
	return name
}

// busiestWindow is the observed window closest to its limit.
func busiestWindow(ws []bus.BudgetWindow) (bus.BudgetWindow, bool) {
	var best bus.BudgetWindow
	ok := false
	for _, w := range ws {
		if windowUnobserved(w) {
			continue
		}
		if !ok || w.UsedPct > best.UsedPct {
			best, ok = w, true
		}
	}
	return best, ok
}

// windowTail is a row's muted end: an aged reading says its age, else the
// reset, else nothing.
func windowTail(w bus.BudgetWindow, b bus.BudgetSnapshot, now time.Time) string {
	switch {
	case windowAged(w, b, now):
		return GlyphNone + " read " + windowAge(w, b, now) + " ago"
	case w.ResetsAt > 0 && time.Unix(w.ResetsAt, 0).After(now):
		return GlyphNone + " resets " + resetClock(time.Unix(w.ResetsAt, 0), now)
	}
	return ""
}

// resetClock is a time ahead as the person reads a clock: HH:MM within a
// day, else the weekday and HH:MM.
func resetClock(t, now time.Time) string {
	if t.Sub(now) < 24*time.Hour {
		return t.Local().Format("15:04")
	}
	return t.Local().Format("Mon 15:04")
}

// paceSentence says whether the card's windows reach their resets at the
// pace they are spent: a spent window first, then the earliest window
// projected past its limit before its reset, else on track. An aged reading
// never feeds a forecast (foureyes): with no fresh window to judge, the card
// says how old its reading is. "" when no window has a pace to judge (no
// reset, an unknown length, nothing spent yet).
func paceSentence(b bus.BudgetSnapshot, now time.Time) string {
	judged := false
	aged := ""
	var limitAt, resetAt, spentUntil time.Time
	for _, w := range b.Windows {
		if windowUnobserved(w) {
			continue
		}
		if windowAged(w, b, now) { // its age is said even when it has no pace (reviewer-codex)
			aged = windowAge(w, b, now)
			continue
		}
		gone := windowElapsedPct(w, now)
		if gone <= 0 {
			continue
		}
		judged = true
		if w.UsedPct >= 100 { // spent: the latest reset is when it frees
			if r := time.Unix(w.ResetsAt, 0); r.After(spentUntil) {
				spentUntil = r
			}
			continue
		}
		if w.UsedPct <= 0 {
			continue
		}
		length := time.Duration(windowMinutes(w)) * time.Minute
		elapsed := time.Duration(float64(length) * gone / 100)
		toLimit := time.Duration(float64(elapsed) * (100 - w.UsedPct) / w.UsedPct) // at the pace so far
		reset := time.Unix(w.ResetsAt, 0)
		if at := now.Add(toLimit); at.Before(reset) && (limitAt.IsZero() || at.Before(limitAt)) {
			limitAt, resetAt = at, reset
		}
	}
	switch {
	case !spentUntil.IsZero():
		return "Limit reached: it resets at " + resetClock(spentUntil, now) + "."
	case !limitAt.IsZero():
		return "At this pace the limit comes around " + resetClock(limitAt, now) + ", before the " + resetClock(resetAt, now) + " reset."
	case judged:
		return "On track: you should reach the reset with room to spare."
	case aged != "":
		return "Reading " + aged + " old: no forecast."
	}
	return ""
}
