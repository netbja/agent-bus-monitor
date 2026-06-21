package main

import (
	"strings"
	"testing"
	"time"
)

func TestStatusBar(t *testing.T) {
	master := statusBar("trading", "hermes")
	if !strings.Contains(master, "⬢ MASTER hermes") {
		t.Fatalf("statusBar(driver) = %q, want '⬢ MASTER hermes'", master)
	}
	if !strings.Contains(master, "trading") {
		t.Fatalf("statusBar = %q, want the project name", master)
	}
	auto := statusBar("trading", "")
	if !strings.Contains(auto, "autonome") || strings.Contains(auto, "MASTER") {
		t.Fatalf("statusBar(\"\") = %q, want 'autonome' and no MASTER", auto)
	}
}

func TestEntryTime(t *testing.T) {
	if got, want := entryTime("1779707136877-0"), time.UnixMilli(1779707136877); !got.Equal(want) {
		t.Fatalf("entryTime = %v, want %v", got, want)
	}
	if entryTime("bogus").IsZero() {
		t.Fatal("entryTime(bogus) returned zero time, want a fallback")
	}
}

func TestSelPos(t *testing.T) {
	feed := []feedLine{{id: "1"}, {id: "2"}, {id: "3"}}
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"1", 0},
		{"3", 2},
		{"99", -1}, // scrolled out of the capped feed
		{"", -1},   // live mode: no selection
	} {
		if got := selPos(feed, tc.id); got != tc.want {
			t.Errorf("selPos(feed, %q) = %d, want %d", tc.id, got, tc.want)
		}
	}
	if got := selPos(nil, "1"); got != -1 {
		t.Errorf("selPos(nil, _) = %d, want -1", got)
	}
}

func TestSelectionTitle(t *testing.T) {
	got := selectionTitle(3, 42)
	if !strings.Contains(got, "3/42") {
		t.Errorf("selectionTitle(3,42) = %q, want it to show 3/42", got)
	}
	if !strings.Contains(got, "copier") {
		t.Errorf("selectionTitle = %q, want it to mention the copy key", got)
	}
}

func TestAgentLabel(t *testing.T) {
	now := time.Now()

	base := &agentState{state: "working", lastSeen: now}
	if got := agentLabel("dev", base, now); !strings.Contains(got, "dev: working") {
		t.Fatalf("agentLabel base = %q, want it to show 'dev: working'", got)
	}
	if strings.Contains(agentLabel("dev", base, now), "👂") {
		t.Fatal("unarmed agent should not show the listening badge")
	}

	armed := &agentState{state: "idle", lastSeen: now, armed: true}
	if got := agentLabel("dev", armed, now); !strings.Contains(got, "👂") {
		t.Fatalf("armed agent label = %q, want a 👂 badge", got)
	}

	// Backlog while listening → yellow ⌛ (normal/transient).
	busy := &agentState{state: "idle", lastSeen: now, armed: true, lag: 2}
	if got := agentLabel("dev", busy, now); !strings.Contains(got, "⌛2") || !strings.Contains(got, "[yellow]") {
		t.Fatalf("armed+lag label = %q, want a yellow ⌛2", got)
	}

	// Backlog with nobody listening → the "stopped listening" tell, orange ⌛.
	dead := &agentState{state: "idle", lastSeen: now, armed: false, lag: 5}
	if got := agentLabel("dev", dead, now); !strings.Contains(got, "⌛5") || !strings.Contains(got, "[orange]") {
		t.Fatalf("unarmed+lag label = %q, want an orange ⌛5 warning", got)
	}

	gated := &agentState{state: "working", lastSeen: now, gated: 1}
	if got := agentLabel("dev", gated, now); !strings.Contains(got, "🔒1") {
		t.Fatalf("gated agent label = %q, want a 🔒1 badge", got)
	}
}
