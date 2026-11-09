package main

import (
	"fmt"
	"sort"
	"strings"
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
	bar := func(fill int, role Role) Line {
		return Line{{Text: "  ", Role: RoleDim}, {Text: strings.Repeat(GlyphThinBar, fill), Role: role},
			{Text: strings.Repeat(GlyphThinBar, progressBarW-fill), Role: RoleMuted}, {Text: "  ", Role: RoleDim}}
	}
	switch {
	case p.n < paceSure: // thin: no share, the end an estimate
		if at := r.Accepted.Add(p.usual); !at.After(now) { // already past: its day when not today (foureyes)
			end = startClock(at, now)
		}
		l = append(l, bar(0, RoleMuted)...)
		return append(l, Seg{Text: "estimate ~" + end, Role: RoleDim}), true
	case used >= p.usual: // past the usual length: held at the cap, muted
		l = append(l, bar(progressCap*progressBarW/100, RoleMuted)...)
		return append(l, Seg{Text: "longer than usual (" + humanAge(p.usual) + ")", Role: RoleDim}), true
	}
	pct := min(int(used*100/p.usual), progressCap)
	l = append(l, bar(pct*progressBarW/100, RoleDone)...)
	return append(l, Seg{Text: fmt.Sprintf("~%d%%", pct), Role: RoleDim}, sep, Seg{Text: "ends ~" + end, Role: RoleDim}), true
}
