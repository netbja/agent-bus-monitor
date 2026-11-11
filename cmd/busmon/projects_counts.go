package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Projects page's two counts (spec 2026-11-09-requests-plan-redesign,
// part 6, the person's Q3): for you, the Work model's YOU rows, and at
// work, its TEAM agents. One counter (correction 3): the same rule as the
// header's, read for every project on the broker.

// workCounts is one project's two numbers.
type workCounts struct{ you, team int }

// countsFresh: past it, another project's counts read ? rather than an old
// number (the person: "? rather than 0 when a remote broker's data is not
// fresh"). The page is read on M and R, never polled.
const countsFresh = time.Minute

// countsReadTimeout bounds one project's count read (two poll ticks, each
// within pollBudget); a test lowers it. countsReadsAtOnce is how many run
// side by side.
var countsReadTimeout = 4 * time.Second

const countsReadsAtOnce = 4

// countsSplitW: a page narrower (its inside, 88 cells at 90 columns) shows
// the two counts in one column, "♙/●", so the project's name keeps its room.
const countsSplitW = 88

// workCountsOf is the Work model's two counts from a poll's snapshot, as the
// shell's refresh computes them (no feed: its unread rows are not YOU's).
func workCountsOf(s snapshot, self string, now time.Time) workCounts {
	w := workOf(workInput{forYou: forYou(s, nil), reqs: requestGroups(requestInputOf(s, self, now)),
		requests: s.requests, plan: s.planState, agents: s.agents, pilot: s.pilot, now: now})
	return workCounts{you: w.youCount(), team: len(w.Team)}
}

// readProjectCounts reads one project as the poll does (two ticks: the
// first reads everything, the throttled reads included, and learns the
// agents; the second reads their gates) and counts it. Any read that fails
// makes it an error (?), never a 0 (reviewer-codex). Every read is a read:
// busReader writes nothing.
func readProjectCounts(ctx context.Context, b busReader, person string, now time.Time) (workCounts, error) {
	r, failed := recording(b)
	st := newPollShared(person)
	pollOnce(ctx, r, st, 1) // every read, the throttled ones included
	pollOnce(ctx, r, st, 2) // the gates of the agents the first tick read (they are read per known agent)
	if !st.health.ok {
		return workCounts{}, errors.New(or(st.health.err, "broker not read"))
	}
	if err := failed(); err != nil { // a kept or missing value would count as 0
		return workCounts{}, err
	}
	return workCountsOf(takeSnapshot(st, now), person, now), nil
}

// newPollShared is an empty shared a poll fills.
func newPollShared(person string) *shared {
	return &shared{person: person, agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{},
		board: map[string]bus.BoardEntry{}, health: health{ok: true}}
}

// withLiveCounts is the page as drawn: the open project's counts are the
// shell's own, live (nil while busmon's link is down: ?).
func withLiveCounts(v adminView, live *workCounts) adminView {
	rows := make([]adminRow, len(v.rows))
	copy(rows, v.rows)
	for i := range rows {
		if rows[i].open && rows[i].onBroker {
			rows[i].counts, rows[i].countsLive = live, true
		}
	}
	v.rows = rows
	return v
}

// countsCell is a count as the page shows it: ? when unknown or no longer
// fresh, — for a template only.
func countsCell(r adminRow, now time.Time, n func(workCounts) int) string {
	if c, ok := brokerCell(r); !ok {
		return c
	}
	if r.counts == nil || (!r.countsLive && now.Sub(r.countsAt) > countsFresh) {
		return "?"
	}
	return strconv.Itoa(n(*r.counts))
}

func adminForYou(r adminRow, now time.Time) string {
	return countsCell(r, now, func(c workCounts) int { return c.you })
}

func adminAtWork(r adminRow, now time.Time) string {
	return countsCell(r, now, func(c workCounts) int { return c.team })
}

// countsNote says the counts are no longer fresh, and how to read them.
func countsNote(v adminView, now time.Time) string {
	for _, r := range v.rows {
		if r.onBroker && !r.countsLive && r.counts != nil && now.Sub(r.countsAt) > countsFresh {
			return fmt.Sprintf("for you and at work: read %s ago, ? past %s; R reads them again", humanAge(now.Sub(r.countsAt)), humanAge(countsFresh))
		}
	}
	return ""
}

// liveCounts is the open project's counts, the shell's Work model; nil
// while busmon's own link to the broker is down.
func (m *Model) liveCounts() *workCounts {
	if !m.snap.health.ok {
		return nil
	}
	return &workCounts{you: m.work.youCount(), team: len(m.work.Team)}
}
