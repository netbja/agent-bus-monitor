package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost"
)

// The current build's holder comes first in List and wins, even when an old
// build's terminal of the agent started later; in one holder, the newest
// live one wins; a closed one, another agent's or project's never.
func TestHolderTermIsTheOneAttachPicks(t *testing.T) {
	term := func(id, build, name string, alive bool) termhost.Term {
		return termhost.Term{ID: termhost.TermID(id), Project: "p", Name: name, Alive: alive, Build: build, PIDStart: 1}
	}
	terms := []termhost.Term{
		term("cur/1", "cur", "foureyes", true),
		term("cur/2", "cur", "coder", true),
		term("cur/3", "cur", "coder", true),
		term("cur/4", "cur", "coder", false),
		term("old/1", "old", "coder", true),
		term("old/2", "old", "master", true),
		{ID: "q/1", Project: "q", Name: "architect", Alive: true, Build: "cur"},
	}
	terms[4].PIDStart = 99 // started last of all: still not the current build's
	if got, ok := holderTerm(terms, "p", "coder"); !ok || got.ID != "cur/3" {
		t.Fatalf("coder: %v %v", got.ID, ok)
	}
	if got, ok := holderTerm(terms, "p", "master"); !ok || got.ID != "old/2" {
		t.Fatalf("master, only on the old holder: %v %v", got.ID, ok)
	}
	for _, name := range []string{"architect", "sentinel"} {
		if _, ok := holderTerm(terms, "p", name); ok {
			t.Fatalf("%s has no terminal in p", name)
		}
	}
	if got, ok := holderTerm(terms[:4], "p", "coder"); !ok || got.ID != "cur/3" {
		t.Fatalf("a closed newest: %v", got.ID)
	}
}
