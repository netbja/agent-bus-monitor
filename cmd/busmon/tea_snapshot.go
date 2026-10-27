package main

import (
	"maps"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// snapshot is the shell's copy of shared, taken under shared.mu by the poll
// command (busmon Charm spec, step b): View reads only this copy and never
// takes the lock. The poller replaces shared's maps and pointers whole rather
// than mutating them, so a shallow copy of each agent's state is enough.
type snapshot struct {
	person, pilot, update, configNote string
	dispatch, missions                string
	health                            health
	guardian                          guardianSummary
	agents                            map[string]*agentState
	budgets                           map[string]bus.BudgetSnapshot
	holds                             map[string]int64
	pace                              map[string]string
	pause                             *bus.PauseRecord
	asks                              []bus.Ask
	board                             map[string]bus.BoardEntry
	requests                          []request
	threads                           []openThread
	plan                              *planSummary
	planState                         planState // the plan itself, for its reader page
	guardianHealth                    map[string]guardian.Record
	guardianNotes                     map[string]guardian.Note
	guardianNow                       time.Time
	svcInst                           *bus.ServicesInstall
	svcRecs                           map[string]bus.ServiceRecord
}

// takeSnapshot copies st at now.
func takeSnapshot(st *shared, now time.Time) snapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	guardNow := st.guardianNow
	if guardNow.IsZero() {
		guardNow = now
	}
	s := snapshot{
		person: st.person, pilot: st.pilot, update: st.update, configNote: st.configNote,
		dispatch: st.dispatch, missions: st.missions, health: st.health,
		guardian: summarizeGuardian(st.guardianHealth, guardNow),
		agents:   make(map[string]*agentState, len(st.agents)),
		budgets:  st.budgets, holds: st.holds, pace: maps.Clone(st.pace.last), pause: st.pause,
		asks: st.asks, board: st.board, requests: st.requests, threads: st.threads,
		guardianHealth: st.guardianHealth, guardianNotes: st.guardianNotes, guardianNow: guardNow,
		svcInst: st.svcInst, svcRecs: st.svcRecs,
	}
	for n, a := range st.agents {
		c := *a
		c.judgedAt = now
		s.agents[n] = &c
	}
	if sum, ok := st.plan.summary(now); ok {
		s.plan = &sum
	}
	// What the plan's pages read: the poller mutates its maps (failed,
	// ledger) in place, so they stay behind; rows is a new slice per Derive.
	p := st.plan
	s.planState = planState{loaded: p.loaded, rec: p.rec, file: p.file, rows: p.rows, partial: p.partial}
	return s
}
