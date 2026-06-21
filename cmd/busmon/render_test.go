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
	if got := agentLabel("dev", base, now, false); !strings.Contains(got, "dev: working") {
		t.Fatalf("agentLabel base = %q, want it to show 'dev: working'", got)
	}
	if strings.Contains(agentLabel("dev", base, now, false), "👂") {
		t.Fatal("unarmed agent should not show the listening badge")
	}

	armed := &agentState{state: "idle", lastSeen: now, armed: true}
	if got := agentLabel("dev", armed, now, false); !strings.Contains(got, "👂") {
		t.Fatalf("armed agent label = %q, want a 👂 badge", got)
	}

	// Backlog while listening → yellow ⌛ (normal/transient).
	busy := &agentState{state: "idle", lastSeen: now, armed: true, lag: 2}
	if got := agentLabel("dev", busy, now, false); !strings.Contains(got, "⌛2") || !strings.Contains(got, "[yellow]") {
		t.Fatalf("armed+lag label = %q, want a yellow ⌛2", got)
	}

	// Backlog with nobody listening → the "stopped listening" tell, orange ⌛.
	dead := &agentState{state: "idle", lastSeen: now, armed: false, lag: 5}
	if got := agentLabel("dev", dead, now, false); !strings.Contains(got, "⌛5") || !strings.Contains(got, "[orange]") {
		t.Fatalf("unarmed+lag label = %q, want an orange ⌛5 warning", got)
	}

	gated := &agentState{state: "working", lastSeen: now, gated: 1}
	if got := agentLabel("dev", gated, now, false); !strings.Contains(got, "🔒1") {
		t.Fatalf("gated agent label = %q, want a 🔒1 badge", got)
	}
}

func TestAgentLabelMaster(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "working", lastSeen: now}
	if got := agentLabel("hermes", a, now, true); !strings.Contains(got, "⬢") {
		t.Fatalf("master label = %q, want a ⬢ marker", got)
	}
	if got := agentLabel("hermes", a, now, false); strings.Contains(got, "⬢") {
		t.Fatalf("non-master label = %q, should not have ⬢", got)
	}
}

func TestPackChips(t *testing.T) {
	// (a) all chips fit on one row
	rows, used := packChips([]chip{{"a", 1}, {"b", 1}, {"c", 1}}, 100, 4)
	if used != 1 || len(rows) != 1 {
		t.Fatalf("(a) used=%d rows=%v, want 1 row", used, rows)
	}
	if !strings.Contains(rows[0], "a") || !strings.Contains(rows[0], "c") {
		t.Fatalf("(a) row missing chips: %q", rows[0])
	}
	// (b) wrap onto a second row when too wide (w=6 each, sep 2, width 10 → 14 > 10)
	_, used = packChips([]chip{{"AAAAAA", 6}, {"BBBBBB", 6}}, 10, 4)
	if used != 2 {
		t.Fatalf("(b) used=%d, want 2 rows", used)
	}
	// (c) overflow past maxRows → "+N" on the last row
	chips := []chip{{"a", 6}, {"b", 6}, {"c", 6}, {"d", 6}, {"e", 6}}
	rows, used = packChips(chips, 6, 2)
	if used != 2 {
		t.Fatalf("(c) used=%d, want 2 (capped)", used)
	}
	if !strings.Contains(rows[1], "+3") {
		t.Fatalf("(c) last row = %q, want a +3 overflow marker", rows[1])
	}
	// (d) packs by visible width w, not byte length (tagged chip is 10 bytes, w=1)
	tagged := chip{"[green]x[-]", 1}
	_, used = packChips([]chip{tagged, tagged, tagged}, 7, 4) // 1+2+1+2+1 = 7 fits
	if used != 1 {
		t.Fatalf("(d) used=%d, want 1 row (packed by visible width)", used)
	}
}
