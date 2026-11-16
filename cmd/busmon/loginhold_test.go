package main

import (
	"strings"
	"testing"
	"time"
)

// busmon's quota line for a login hold (login-updates r9 §3.6): one line per
// held agent, sorted, with the expiry in the host's zone; none without one.
func TestLoginHoldLines(t *testing.T) {
	exp := time.Date(2026, 11, 19, 15, 40, 0, 0, time.Local)
	agents := map[string]*agentState{
		"coder":    {loginHeld: exp.UnixMilli()},
		"foureyes": {},
		"arch":     {loginHeld: exp.Add(-24 * time.Hour).UnixMilli()},
	}
	got := linesText(loginHoldLines(agents))
	want := "arch: Claude login expired Wed 15:40, held\ncoder: Claude login expired Thu 15:40, held"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if ls := loginHoldLines(map[string]*agentState{"coder": {}}); len(ls) != 0 {
		t.Fatalf("no hold: %q", linesText(ls))
	}
	marks := map[string]string{"hold-login:coder": "1794777000000", "hold-login:bad": "x", "hold:anthropic": "1"}
	if loginHoldOf(marks, "coder") != 1794777000000 || loginHoldOf(marks, "bad") != 0 || loginHoldOf(marks, "kimi") != 0 {
		t.Fatal("loginHoldOf")
	}
}

// The Accounts section of the tokens page lists a login-held agent under the cards.
func TestTokensPageShowsALoginHold(t *testing.T) {
	exp := time.Date(2026, 11, 19, 15, 40, 0, 0, time.Local)
	agents := map[string]*agentState{"coder": {loginHeld: exp.UnixMilli()}}
	page := linesText(tokensPage(iconsOff, tokState{}, nil, nil, agents, "agent-bus-monitor", exp, 120, false, barView{}, true))
	if !strings.Contains(page, "  coder: Claude login expired Thu 15:40, held") {
		t.Fatalf("no login hold line:\n%s", page)
	}
}
