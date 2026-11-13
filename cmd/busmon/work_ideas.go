package main

import (
	"context"

	"github.com/netbja/agent-bus-monitor/bus"
)

// brokerIdeasOf reads the project's ideas from the broker ({p}:ideas, ideas
// slice 1): the same list on every host, a laptop's busmon too. Read-only.
func brokerIdeasOf(ctx context.Context, b *bus.Bus) func() ([]bus.Idea, error) {
	return func() ([]bus.Idea, error) {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		return b.Ideas(cc)
	}
}

// readIdeas refreshes the shared ideas at the requests' cadence, read before
// the lock. A failed read keeps the last list and flags it.
func readIdeas(st *shared) {
	if st.ideasOf == nil {
		return
	}
	ideas, err := st.ideasOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.ideasErr = err != nil
	if err == nil {
		st.ideas = ideas
	}
}

// workInput is the model's input from the snapshot and the views refresh
// already computed.
func (m *Model) workInput() workInput {
	s := m.snap
	return workInput{forYou: m.forYou, reqs: m.lists.reqView, requests: s.requests, plan: s.planState,
		agents: s.agents, pilot: s.pilot, ideas: s.ideas, ideasErr: s.ideasErr, now: m.now}
}
