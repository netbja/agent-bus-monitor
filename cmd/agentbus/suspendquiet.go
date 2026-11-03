package main

import (
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The suspend's quiet rule (spec docs/superpowers/specs/2026-11-03-suspend-
// resume-design.md §3.1): with the pause written, no new wake can be reserved,
// so the project goes quiet once every running turn has ended and every wake
// reserved before the pause has been typed and seen answered. suspendQuiet is
// the one predicate every round uses; nothing else judges.

// quietSettle is how long after a wake's boundary an inspection must be taken
// to count: longer than a client's turn start after its input.
const quietSettle = 5 * time.Second

// quietReadyRounds is how many consecutive ready inspections after the
// boundary answer a wake, and how many quiet rounds in a row make the project
// quiet.
const quietReadyRounds = 2

// quietFacts is one round's reading, every time in Unix ms from Redis TIME.
type quietFacts struct {
	Now    int64
	Agents map[string]bus.AgentSnapshot
	Wakes  map[string]bus.WakeRecord   // the agents with a wake record
	Obs    map[string]*bus.Observation // the suspend's own inspection this round (nil = none)
	Gone   map[string]bool             // the verified process proved gone (dispatch.ProcGone)
	// DispatchGoneAt is when the dispatcher was first proved gone (its service
	// inactive and its lease absent, both read after the pause); 0 = not proved.
	DispatchGoneAt int64
	StaleAfter     int64 // dispatch.turn_stale_after, for EffectiveState
}

// busyAgent is an agent the project waits for, and why.
type busyAgent struct{ Agent, Why string }

// quietState is what the rule carries from round to round.
type quietState struct {
	boundary map[string]int64 // agent -> B, the first moment its wake's input can arrive no more
	ready    map[string]int   // agent -> consecutive ready inspections at or after B + settle
	quiet    int              // consecutive rounds with nobody busy
	settle   time.Duration    // quietSettle; tests shorten it
}

func newQuietState() *quietState {
	return &quietState{boundary: map[string]int64{}, ready: map[string]int{}, settle: quietSettle}
}

// typed is a wake record whose input may have reached the pane: being typed,
// typed, or typed with its marker unseen (unobserved counts as sent).
func typed(state string) bool {
	return state == "prompting" || state == "prompted" || state == "unobserved"
}

// judge reads one round and returns the agents still busy, sorted.
func (s *quietState) judge(f quietFacts) []busyAgent {
	names := make([]string, 0, len(f.Agents))
	for a := range f.Agents {
		names = append(names, a)
	}
	sort.Strings(names)
	var busy []busyAgent
	for _, a := range names {
		snap := f.Agents[a]
		if f.Now-snap.TS > bus.DormantAfter.Milliseconds() || f.Gone[a] {
			continue // dormant, or its process gone: no turn can run (reviewer-codex)
		}
		obs := f.Obs[a]
		fresh := obs != nil && obs.Fresh(f.Now)
		ready := fresh && !obs.Busy && (obs.State == "idle" || obs.State == "done")
		if w, ok := f.Wakes[a]; ok && typed(w.State) {
			if why := s.wake(a, w, snap, obs, ready, f); why != "" {
				busy = append(busy, busyAgent{a, why})
				continue
			}
		}
		if st, _ := bus.EffectiveState(snap, obs, f.Now, f.StaleAfter); st == "working" {
			busy = append(busy, busyAgent{a, "working"})
		} else if fresh && obs.Busy {
			busy = append(busy, busyAgent{a, "pane busy"})
		}
	}
	return busy
}

// wake judges a wake record that may have typed: "" once answered. Its
// boundary B is never the record's At (the reservation's time, which a later
// prompt does not move, reviewer-codex): it is the suspend's own first read
// out of prompting, or the dispatcher's proved exit. No age releases it.
func (s *quietState) wake(a string, w bus.WakeRecord, snap bus.AgentSnapshot, obs *bus.Observation, ready bool, f quietFacts) string {
	b, known := s.boundary[a]
	if !known {
		switch {
		case w.State != "prompting":
			b = f.Now
		case f.DispatchGoneAt > 0:
			b = f.DispatchGoneAt
		default:
			s.ready[a] = 0
			return "wake in flight"
		}
		s.boundary[a] = b
	}
	if ready && obs.At >= b+s.settle.Milliseconds() {
		s.ready[a]++
	} else {
		s.ready[a] = 0
	}
	if (snap.State != "working" && snap.TS > b) || s.ready[a] >= quietReadyRounds {
		return ""
	}
	return "woken, turn not seen ending"
}

// round judges one round and says whether the project is quiet: nobody busy
// in quietReadyRounds rounds in a row.
func (s *quietState) round(f quietFacts) ([]busyAgent, bool) {
	busy := s.judge(f)
	if len(busy) > 0 {
		s.quiet = 0
		return busy, false
	}
	s.quiet++
	return nil, s.quiet >= quietReadyRounds
}
