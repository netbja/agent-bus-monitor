package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// The progress line (progress-line §3): under a task in hand, when it
// started, the time it has used against the usual length of its owner's
// tasks, and when it should end. busmon cannot see the work itself: the
// share is time used, marked "~", and "estimate" when there is little to go
// on.

const (
	paceWindow   = 10 // the owner's last done requests the pace reads
	paceSure     = 5  // below this many, the end is an "estimate"
	progressCap  = 95 // an open task never reads more than this share
	progressBarW = 10 // the bar's cells
)

// pace is an owner's usual task length: the median accepted -> done of its
// last paceWindow done tracked requests still on the board (a dropped round
// is gone with its times), and how many it read.
type pace struct {
	usual time.Duration
	n     int
}

// reqOwner is who a request is with: its target, else its board owner.
func reqOwner(r request) string {
	if r.Target != "" {
		return r.Target
	}
	return r.Owner
}

// ownerPaces is each owner's pace from the board's requests.
func ownerPaces(reqs []request) map[string]pace {
	done := map[string][]request{}
	for _, r := range reqs {
		if r.Tracked && r.State == "done" && !r.Accepted.IsZero() && r.Responded.After(r.Accepted) {
			done[reqOwner(r)] = append(done[reqOwner(r)], r)
		}
	}
	out := map[string]pace{}
	for owner, rs := range done {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Responded.After(rs[j].Responded) })
		rs = rs[:min(len(rs), paceWindow)]
		ds := make([]time.Duration, len(rs))
		for i, r := range rs {
			ds[i] = r.Responded.Sub(r.Accepted)
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		mid := len(ds) / 2
		usual := ds[mid]
		if len(ds)%2 == 0 {
			usual = (ds[mid-1] + ds[mid]) / 2
		}
		out[owner] = pace{usual: usual, n: len(ds)}
	}
	return out
}

// startClock is a past time as a clock: HH:MM today, else the weekday too.
func startClock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// progressLine is the line under a task in hand: r accepted, p its owner's
// pace. ok false when r has not started.
func progressLine(r request, p pace, now time.Time) (Line, bool) {
	if r.Accepted.IsZero() {
		return nil, false
	}
	sep := Seg{Text: "  " + GlyphNone + "  ", Role: RoleDim}
	l := Line{{Text: "started " + startClock(r.Accepted, now), Role: RoleDim}}
	if p.n == 0 {
		return append(l, sep, Seg{Text: "no estimate yet", Role: RoleDim}), true
	}
	used := max(now.Sub(r.Accepted), 0)
	end := resetClock(r.Accepted.Add(p.usual), now)
	bar := func(pct float64, role Role) Line {
		return append(append(Line{{Text: "  ", Role: RoleDim}}, progressBar(pct, progressBarW, role)...), Seg{Text: "  ", Role: RoleDim})
	}
	switch {
	case p.n < paceSure: // thin: no share, the end an estimate
		if at := r.Accepted.Add(p.usual); !at.After(now) { // already past: its day when not today (foureyes)
			end = startClock(at, now)
		}
		l = append(l, bar(0, RoleMuted)...)
		return append(l, Seg{Text: "estimate ~" + end, Role: RoleDim}), true
	case used >= p.usual: // past the usual length: held at the cap, muted
		l = append(l, bar(progressCap, RoleMuted)...)
		return append(l, Seg{Text: "longer than usual (" + humanAge(p.usual) + ")", Role: RoleDim}), true
	}
	pct := progressPct(used, p.usual)
	l = append(l, bar(pct, RoleDone)...)
	return append(l, Seg{Text: fmt.Sprintf("~%.0f%%", math.Floor(pct)), Role: RoleDim}, sep, Seg{Text: "ends ~" + end, Role: RoleDim}), true
}

// progressPct is the share of the usual length used, capped at progressCap.
func progressPct(used, usual time.Duration) float64 {
	return min(float64(used)*100/float64(usual), progressCap)
}

// progressBar is the progress line's bar (tui-rendering §4.4): finedraw's
// Bar, so it moves by eighths of a cell in fine (10 cells, 80 steps), whole
// cells in basic, # and - in ascii; the fill in role, its empty part dim.
func progressBar(pct float64, cells int, role Role) Line {
	return gaugeSegs(pct, cells, role, false)
}

// progressBrief is a kanban card's progress (tui-rendering §4.4): a short
// bar and "~N%"; "estimate" while the pace is thin, "longer than usual"
// past it; ok false when r has not started or its owner has no pace.
func progressBrief(r request, p pace, now time.Time) (Line, bool) {
	if r.Accepted.IsZero() || p.n == 0 {
		return nil, false
	}
	used := max(now.Sub(r.Accepted), 0)
	lead := Line{{Text: "  ", Role: RoleDim}}
	switch {
	case p.n < paceSure:
		return append(append(lead, progressBar(0, kanbanBarW, RoleMuted)...), Seg{Text: " estimate", Role: RoleDim}), true
	case used >= p.usual:
		return append(append(lead, progressBar(progressCap, kanbanBarW, RoleMuted)...), Seg{Text: " longer than usual", Role: RoleDim}), true
	}
	pct := progressPct(used, p.usual)
	return append(append(lead, progressBar(pct, kanbanBarW, RoleDone)...), Seg{Text: fmt.Sprintf(" ~%.0f%%", math.Floor(pct)), Role: RoleDim}), true
}

// kanbanBarW is an In progress card's bar.
const kanbanBarW = 6
