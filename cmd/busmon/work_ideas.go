package main

import (
	"context"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/memory"
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

// localNotesOf reads the idea notes (memory.OpenQuestions) in the project's
// checkout on this machine, the template's working_dir; nil when there is
// none (a laptop's busmon: the broker's ideas only). Read-only.
func localNotesOf(project string, getenv func(string) string) func() ([]memory.Question, error) {
	root := projectCheckout(project, getenv)
	if root == "" {
		return nil
	}
	return func() ([]memory.Question, error) { return memory.OpenQuestions(root) }
}

// readIdeas refreshes the shared ideas and idea notes at the requests'
// cadence, each read before the lock. A failed read keeps the last list and
// flags it.
func readIdeas(st *shared) {
	if st.ideasOf != nil {
		ideas, err := st.ideasOf()
		st.mu.Lock()
		st.ideasErr = err != nil
		if err == nil {
			st.ideas = ideas
		}
		st.mu.Unlock()
	}
	if st.notesOf != nil {
		notes, err := st.notesOf()
		st.mu.Lock()
		st.notesErr = err != nil
		if err == nil {
			st.notes = notes
		}
		st.mu.Unlock()
	}
}

// workInput is the model's input from the snapshot and the views refresh
// already computed.
func (m *Model) workInput() workInput {
	s := m.snap
	return workInput{forYou: m.forYou, reqs: m.lists.reqView, requests: s.requests, plan: s.planState,
		agents: s.agents, pilot: s.pilot, ideas: s.ideas, ideasErr: s.ideasErr, notes: s.notes, notesErr: s.notesErr, now: m.now}
}
