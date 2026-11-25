package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// observer-role (the person, 2026-11-25): an observer session reads the bus
// and writes nothing. With AGENT_BUS_AGENT naming an observer of the
// manifest, the CLI allows only the read forms and refuses every other
// command before the bus, naming the rule; any other identity is untouched.
func TestObserverGuard(t *testing.T) {
	isObs := func(n string) bool { return n == "observer" }
	allowed := [][]string{
		{"agents"}, {"agents", "coder", "--json"}, {"board"}, {"board", "--json"}, {"request"}, {"request", "--json"},
		{"reports"}, {"reports", "--unread", "--to", "master"}, {"reports", "1795-0"}, {"thread", "1795-0"},
		{"history", "--agent", "coder"}, {"verdicts", "--subject", "x"}, {"asks", "--open"}, {"swaps"},
		{"health"}, {"health", "list"}, {"health", "detail", "h1"}, {"health", "log"}, {"health", "warns"},
		{"quota"}, {"usage"}, {"budget", "--json"}, {"tokens", "--all"}, {"models"}, {"merge-queue", "--json"},
		{"audit", "--action", "merge"}, {"audit", "verify"}, {"audit", "export", "--since", "1d"}, {"version"}, {"limits"}, {"help"},
	}
	refused := [][]string{
		{"status", "observer", "working", "x"}, {"report", "observer", "--to", "master", "x"}, {"cmd", "coder", "x"},
		{"reply", "1-0", "x"}, {"notify", "x"}, {"ask", "x"}, {"answer", "q", "yes"},
		{"request", "send", "t", "coder", "x"}, {"request", "accept", "t"}, {"board", "claim", "t"}, {"board", "done", "t"},
		{"challenge", "foureyes", "x"}, {"verdict", "approve", "x"}, {"gate", "close", "foureyes", "r"},
		{"deliver"}, {"pop", "security"}, {"retire", "security"}, {"agents", "forget", "coder"},
		{"config", "set", "x", "1"}, {"subscribe", "observer"}, {"listen"}, {"pause"}, {"resume"},
		{"health", "note", "h1", "x"}, {"health", "accept", "h1", "--as", "p", "x"}, {"usage", "coder", "{}"},
		{"budget", "anthropic", "{}"}, {"merge-queue", "drop", "abc", "x"}, {"run", "reset"}, {"models", "refresh"}, {"models", "--refresh"},
		{"swap", "coder", "--profile", "x"}, {"setup"}, {"backup"}, {"plan", "note", "x"}, {"idea", "add", "x"},
		{"handoff"}, {"checkback", "1h"}, {"background", "x"},
	}
	for _, args := range allowed {
		if why := observerRefusal(args, "observer", isObs); why != "" {
			t.Errorf("%v refused to an observer: %s", args, why)
		}
	}
	for _, args := range refused {
		why := observerRefusal(args, "observer", isObs)
		if why == "" || !strings.Contains(why, "observer") || !strings.Contains(why, "reads only") {
			t.Errorf("%v allowed to an observer (%q)", args, why)
		}
	}
	for _, id := range []string{"coder", "master", ""} {
		if why := observerRefusal([]string{"cmd", "coder", "x"}, id, isObs); why != "" {
			t.Errorf("%q refused: %s", id, why)
		}
	}
	if why := observerRefusal([]string{"cmd", "coder", "x"}, "observer", nil); why != "" {
		t.Errorf("no manifest known, refused: %s", why)
	}
}

// The guard main runs: the environment's identity, the manifest ROLES_TOML
// names (here the repository's, which declares observer).
func TestObserverGuardFromTheEnvironment(t *testing.T) {
	wd, _ := os.Getwd()
	t.Setenv("ROLES_TOML", filepath.Join(wd, "..", "..", "roles.toml"))
	t.Cleanup(func() { bus.SetObservers(nil) })
	t.Setenv("AGENT_BUS_AGENT", "observer")
	if code, refused := observerGuard([]string{"cmd", "coder", "x"}); !refused || code != 2 {
		t.Fatalf("an observer's cmd: %d %v", code, refused)
	}
	if _, refused := observerGuard([]string{"board"}); refused {
		t.Fatal("an observer's board refused")
	}
	t.Setenv("AGENT_BUS_AGENT", "coder")
	if _, refused := observerGuard([]string{"cmd", "coder", "x"}); refused {
		t.Fatal("coder's cmd refused")
	}
}
