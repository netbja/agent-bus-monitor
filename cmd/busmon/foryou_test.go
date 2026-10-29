package main

import (
	"strings"
	"testing"
	"time"
)

func TestForYouHint(t *testing.T) {
	cases := []struct {
		state, msg, person string
		want               bool
	}{
		{"idle", "lot 1 delivered; awaiting charlie", "charlie", true},
		{"idle", "Lot 1 delivered. Awaiting Charlie.", "charlie", true},
		{"idle", "over to charlie: review PR 12", "charlie", true},
		{"idle", "waiting for charlies", "charlie", false},
		{"idle", "see https://example.com/charlie/x", "charlie", false},
		{"idle", "notes in /srv/charlie/notes.md", "charlie", false},
		{"idle", "nothing to do", "charlie", false},
		{"waiting", "awaiting charlie", "charlie", false}, // waiting says it already
		{"working", "awaiting charlie", "charlie", false},
		{"idle", "piloted by master; awaiting pilot", "pilot", false}, // the default identity is a bus word
		{"idle", "awaiting charlie", "", false},
	}
	for _, c := range cases {
		if got := forYouHint(c.state, c.msg, c.person); got != c.want {
			t.Errorf("forYouHint(%q, %q, %q) = %v, want %v", c.state, c.msg, c.person, got, c.want)
		}
	}
}

func TestForYouFlagOnlyHints(t *testing.T) {
	a := &agentState{state: "idle", message: "awaiting charlie", forYou: true}
	fs := agentFlags(a, false)
	if len(fs) == 0 || fs[0].Word != "for you" || fs[0].Role != RoleAttention {
		t.Fatalf("flags %+v", fs)
	}
	// A hint never makes the agent wait for the person: no bell, no count.
	if waitsForPerson(a) {
		t.Fatal("the hint counts as a wait")
	}
	if _, word, _, _ := stateLook(a); word != "idle" {
		t.Fatalf("declared state relabelled %q", word)
	}
}

func TestForYouDetailLine(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "idle", message: "awaiting charlie", stateAt: now, forYou: true}
	_, body := agentDetail("master", a, false, nil, nil, now)
	if !strings.Contains(plainOf(body), "It names you but declared idle: no question is open.") {
		t.Fatalf("detail:\n%s", plainOf(body))
	}
	a.forYou = false
	if _, body := agentDetail("master", a, false, nil, nil, now); strings.Contains(plainOf(body), "It names you") {
		t.Fatal("detail line without the hint")
	}
}
