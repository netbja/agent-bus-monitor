package main

import "github.com/netbja/agent-bus-monitor/memory"

// localIdeasOf reads IDEAS (requests-plan-redesign Q2) from the project's
// working tree on this host: the template's working_dir, as the trust line
// finds it. No template: no ideas, no error. Read-only.
func localIdeasOf(project string, env func(string) string) func() ([]memory.Question, error) {
	dir := templateWorkingDir(project, env)
	return func() ([]memory.Question, error) {
		root, ok := dir()
		if !ok {
			return nil, nil
		}
		return memory.OpenQuestions(root)
	}
}

// readIdeas refreshes the shared IDEAS at the requests' cadence, the files
// read before the lock. A failed read keeps the last list and flags it.
func readIdeas(st *shared) {
	if st.ideasOf == nil {
		return
	}
	qs, err := st.ideasOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.ideasErr = err != nil
	if err == nil {
		st.ideas = qs
	}
}

// workInput is the model's input from the snapshot and the views refresh
// already computed.
func (m *Model) workInput() workInput {
	s := m.snap
	return workInput{forYou: m.forYou, reqs: m.lists.reqView, requests: s.requests, plan: s.planState,
		agents: s.agents, pilot: s.pilot, ideas: s.ideas, ideasErr: s.ideasErr, now: m.now}
}
