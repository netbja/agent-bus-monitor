package main

import (
	"context"
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
		if why := observerRefusal(args, "observer", noAncestor, isObs); why != "" {
			t.Errorf("%v refused to an observer: %s", args, why)
		}
	}
	for _, args := range refused {
		why := observerRefusal(args, "observer", noAncestor, isObs)
		if why == "" || !strings.Contains(why, "observer") || !strings.Contains(why, "reads only") {
			t.Errorf("%v allowed to an observer (%q)", args, why)
		}
	}
	for _, id := range []string{"coder", "master", ""} {
		if why := observerRefusal([]string{"cmd", "coder", "x"}, id, noAncestor, isObs); why != "" {
			t.Errorf("%q refused: %s", id, why)
		}
	}
	if why := observerRefusal([]string{"cmd", "coder", "x"}, "observer", noAncestor, nil); why != "" {
		t.Errorf("no manifest known, refused: %s", why)
	}
}

// The guard main runs: the environment's identity, the manifest ROLES_TOML
// names (the repository's with an observer added, as the person's own
// manifest declares it until the repository's can: option a).
func TestObserverGuardFromTheEnvironment(t *testing.T) {
	wd, _ := os.Getwd()
	repo, err := os.ReadFile(filepath.Join(wd, "..", "..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(manifest, append(repo, "\n[roles.observer]\nkind = \"observer\"\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROLES_TOML", manifest)
	t.Cleanup(func() { bus.SetObservers(nil) })
	t.Setenv("AGENT_BUS_AGENT", "observer")
	if code, refused := observerGuard([]string{"cmd", "coder", "x"}, noAncestorOf); !refused || code != 2 {
		t.Fatalf("an observer's cmd: %d %v", code, refused)
	}
	if _, refused := observerGuard([]string{"board"}, noAncestorOf); refused {
		t.Fatal("an observer's board refused")
	}
	t.Setenv("AGENT_BUS_AGENT", "coder")
	if _, refused := observerGuard([]string{"cmd", "coder", "x"}, noAncestorOf); refused {
		t.Fatal("coder's cmd refused")
	}
	// reviewer-codex: no identity, or another's, under a registered observer.
	underObserver := func(context.Context) (string, error) { return "observer", nil }
	for _, id := range []string{"", "coder"} {
		t.Setenv("AGENT_BUS_AGENT", id)
		if code, refused := observerGuard([]string{"cmd", "coder", "x"}, underObserver); !refused || code != 2 {
			t.Errorf("identity %q under the observer: %d %v", id, code, refused)
		}
		if _, refused := observerGuard([]string{"board"}, underObserver); refused {
			t.Errorf("identity %q under the observer: a read refused", id)
		}
	}
}

func noAncestor() string { return "" }

// reviewer-codex: a process under a registered observer is limited whatever
// its own identity (none, or another's); its ancestry is read only for a
// write, never for a read.
func TestObserverGuardByAncestry(t *testing.T) {
	isObs := func(n string) bool { return n == "observer" }
	calls := 0
	underObserver := func() string { calls++; return "observer" }
	for _, id := range []string{"", "coder", "master"} {
		why := observerRefusal([]string{"cmd", "coder", "x"}, id, underObserver, isObs)
		if !strings.Contains(why, "observer") || !strings.Contains(why, "reads only") {
			t.Errorf("identity %q under an observer: %q", id, why)
		}
	}
	calls = 0
	if why := observerRefusal([]string{"board"}, "", underObserver, isObs); why != "" || calls != 0 {
		t.Fatalf("a read under an observer: %q, ancestry read %d times", why, calls)
	}
	if why := observerRefusal([]string{"cmd", "coder", "x"}, "", func() string { return "coder" }, isObs); why != "" {
		t.Fatalf("under coder: %q", why)
	}
	if why := observerRefusal([]string{"cmd", "coder", "x"}, "", underObserver, func(string) bool { return false }); why != "" {
		t.Fatalf("no observer in the manifest: %q", why)
	}
}

func noAncestorOf(context.Context) (string, error) { return "", nil }
