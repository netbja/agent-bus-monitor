package main

import "github.com/netbja/agent-bus-monitor/termhost"

// holderTerm is the terminal of agent that agentbus attach attaches to
// (findAgentTerm): termhost.Host.List gives each holder's terminals in their
// start order, holder after holder, the current build's first; the first
// holder with a live terminal of agent wins, and in it the newest one. busmon
// shows and attaches the same terminal, whatever their start times.
func holderTerm(terms []termhost.Term, project, agent string) (termhost.Term, bool) {
	var found termhost.Term
	ok := false
	for _, t := range terms {
		if ok && t.Build != found.Build {
			break // the first holder that has one is the one
		}
		if t.Project == project && t.Name == agent && t.Alive {
			found, ok = t, true
		}
	}
	return found, ok
}
