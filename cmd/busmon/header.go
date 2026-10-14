package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// headerData is everything the header reports (spec §3.2).
type headerData struct {
	project  string
	health   health
	dispatch string // "", "up", "down", or a service state (serviceFact)
	missions string // the same; shown only when not ""
	pilot    string // pilot-lease driver, "" = autonomous
	self     string // the person busmon writes as (humanLabel); "" = not shown
	update   string // a newer release tag ("" = none): the last, lowest-priority item
	waiting  string // waitingLine: "3 waiting · oldest 42m", "" when nothing waits
	asks     int    // agents in state waiting: decisions a person owes
	budgets  map[string]bus.BudgetSnapshot
	now      time.Time
}

// hItem is one header element. Elements are admitted by prio (1 first) while
// they fit, then drawn in display order; the project alone may be truncated.
type hItem struct {
	line  Line
	prio  int
	row   int  // 0 or 1
	trunc bool // may be cut with "…" down to minProject cells
	right bool // right-aligned on its row (the clock)
	order int  // display order
}

const minProject = 6

// headerLines returns 1 line (wide) or 2 (medium, narrow), each exactly width
// cells. Health and dispatch are admitted first and use compact forms below 80
// columns; then the project (≥ 6 cells or dropped), pilot, waiting, budgets
// (the rightmost provider dropped first) and the clock.
func headerLines(d headerData, width int, tier Tier) []Line {
	compact := width < 80
	rows := 2
	if tier == TierWide {
		rows = 1
	}
	second := 0 // row of the secondary elements
	if rows == 2 {
		second = 1
	}
	gap := 2
	if tier == TierWide {
		gap = 3
	}

	var items []hItem
	add := func(l Line, prio, row int) *hItem {
		items = append(items, hItem{line: l, prio: prio, row: row, order: len(items)})
		return &items[len(items)-1]
	}
	add(Line{{Text: Preview(d.project), Role: RoleText, Bold: true}}, 3, 0).trunc = true
	add(healthSeg(d.health, d.now, compact), 1, 0)
	add(dispatchSeg(d.dispatch, compact), 2, 0)
	if d.missions != "" {
		add(missionsSeg(d.missions, compact), 2, 0)
	}
	if !d.health.ok && d.health.err != "" {
		add(Line{{Text: "(" + Preview(d.health.err) + ")", Role: RoleMuted}}, 9, second)
	}
	if d.asks > 0 {
		add(Line{{Text: fmt.Sprintf("%s %d waiting for you", GlyphHuman, d.asks), Role: RoleAttention}}, 2, 0)
	}
	if d.self != "" {
		add(Line{{Text: Preview(humanLabel(d.self, d.pilot)), Role: RoleText}}, 4, second)
	}
	if d.pilot != "" && d.pilot != d.self {
		add(Line{{Text: "pilot ", Role: RoleMuted}, {Text: Preview(d.pilot), Role: RoleText}}, 4, second)
	} else if d.pilot == "" {
		add(Line{{Text: "autonomous", Role: RoleMuted}}, 4, second)
	}
	if d.waiting != "" {
		add(Line{{Text: Preview(d.waiting), Role: RoleAttention}}, 5, second)
	}
	names := make([]string, 0, len(d.budgets))
	for n := range d.budgets {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		add(budgetSeg(n, d.budgets[n], tier), 6+i, second) // the rightmost provider has the lowest priority
	}
	if d.update != "" { // a newer release: the first item to go when space is short
		add(Line{{Text: GlyphUp + " " + Preview(d.update), Role: RoleMuted}}, 101, second)
	}
	clock := add(Line{{Text: d.now.Format("15:04:05"), Role: RoleMuted}}, 100, 0)
	clock.right = true

	// Admission: cheapest-first by priority, per row. Each row starts with a
	// 1-cell margin; every admitted element after the first costs a gap.
	used := make([]int, rows)
	count := make([]int, rows)
	admitted := make([]bool, len(items))
	cut := make([]int, len(items)) // truncation width, 0 = whole
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return items[idx[a]].prio < items[idx[b]].prio })
	for _, i := range idx {
		it := items[i]
		r := min(it.row, rows-1)
		cost := it.line.Width()
		g := 0
		if count[r] > 0 {
			g = gap
		}
		room := width - 1 - used[r] - g
		switch {
		case cost <= room:
		case it.trunc && room >= minProject:
			cut[i], cost = room, room
		default:
			continue
		}
		admitted[i] = true
		used[r] += g + cost
		count[r]++
	}

	out := make([]Line, rows)
	for r := 0; r < rows; r++ {
		left := Line{{Text: " "}}
		var right Line
		first := true
		for i, it := range items {
			if !admitted[i] || min(it.row, rows-1) != r {
				continue
			}
			l := it.line
			if cut[i] > 0 {
				l = FitLine(l, cut[i])
			}
			if it.right {
				right = l
				continue
			}
			if !first {
				left = append(left, Seg{Text: strings.Repeat(" ", gap)})
			}
			left = append(left, l...)
			first = false
		}
		if right != nil {
			pad := max(1, width-left.Width()-right.Width()-1)
			left = append(left, Seg{Text: strings.Repeat(" ", pad)})
			left = append(left, right...)
		}
		out[r] = FitLine(left, width)
	}
	return out
}

func healthSeg(h health, now time.Time, compact bool) Line {
	if h.ok {
		if compact {
			return Line{{Text: GlyphWorking + " bus", Role: RoleOK}}
		}
		return Line{{Text: GlyphWorking + " bus ok", Role: RoleOK}}
	}
	age := ""
	if !h.since.IsZero() {
		age = " " + humanAge(now.Sub(h.since))
	}
	if compact {
		return Line{{Text: GlyphBlocked + " bus" + age, Role: RoleError}}
	}
	return Line{{Text: GlyphBlocked + " bus down" + age, Role: RoleError}}
}

// dispatchSeg is the dispatcher's state: up/down from its lease, or the
// service's own state (ready, waiting herdr, failed, …) since v0.12.
func dispatchSeg(state string, compact bool) Line {
	return serviceSeg("dispatch", "disp", state, compact)
}

// budgetRole warns before the wall: past 75 % there is time to finish, past 90 % not.
func budgetRole(pct float64) Role {
	switch {
	case pct >= 90:
		return RoleError
	case pct >= 75:
		return RoleAttention
	}
	return RoleOK
}

// budgetSeg is one provider's USED share of its account windows: gauges in
// wide (10 cells) and medium (5), plain "provider 25%/44%" in narrow.
func budgetSeg(name string, b bus.BudgetSnapshot, tier Tier) Line {
	name = Preview(name)
	if tier == TierNarrow {
		return Line{{Text: fmt.Sprintf("%s %.0f%%/%.0f%%", name, b.SessionPct, b.WeeklyPct), Role: budgetRole(max(b.SessionPct, b.WeeklyPct))}}
	}
	cells := 10
	sep := "  "
	if tier == TierMedium {
		cells, sep = 5, " "
	}
	l := Line{{Text: name + " used" + sep, Role: RoleMuted}}
	gauge := func(label string, pct float64) {
		n := clamp(int(pct*float64(cells)/100+0.5), 0, cells)
		l = append(l, Seg{Text: label + " ", Role: RoleMuted},
			Seg{Text: strings.Repeat(GlyphGauge, n), Role: budgetRole(pct)},
			Seg{Text: strings.Repeat(GlyphBarEmpty, cells-n), Role: RoleDim},
			Seg{Text: fmt.Sprintf(" %.0f%%", pct), Role: RoleText})
	}
	gauge("session", b.SessionPct)
	l = append(l, Seg{Text: sep})
	gauge("week", b.WeeklyPct)
	return l
}

// missionsSeg is the conductor's state, next to the dispatcher's.
func missionsSeg(state string, compact bool) Line {
	if state == "" {
		state = "down"
	}
	return serviceSeg("missions", "miss", state, compact)
}
