package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var detNow = time.Date(2026, 10, 10, 18, 30, 0, 0, time.UTC)

func richAgent() *agentState {
	return &agentState{
		state: "blocked", message: "needs review of PR #46\nsecond line\twith tab",
		stateAt: detNow.Add(-6 * time.Minute), lastSeen: detNow.Add(-time.Minute),
		gated: 1, armed: true, lag: 40, pane: "w3:p1", pid: 41822, pidStart: 99,
		waiting: 2, stale: 1, gap: true, queueKnown: true, queueAt: detNow.Add(-3 * time.Second),
		ctxRaw: "608k ctx",
		wake:   &bus.WakeRecord{State: "prompted", Tries: 2, Escalated: "pending", Reason: "pane focused"},
	}
}

func TestAgentDetailFieldsAndAbsent(t *testing.T) {
	board := map[string]bus.BoardEntry{"pr-46-review": {Owner: "grok", State: "blocked", Branch: "fix/pr-46"},
		"other": {Owner: "kimi", State: "working"}}
	title, body := agentDetail("grok", richAgent(), true, board, nil, detNow)
	if !strings.Contains(title, "grok") {
		t.Fatalf("title %q", title)
	}
	for _, want := range []string{
		"needs review of PR #46", "second line", // full message, multi-line
		"Declared", "blocked", "6m ago",
		"Last seen", "1m ago",
		"Pane", "w3:p1", "pid 41822",
		"Receiver lease", "armed",
		"Queue", "2 waiting", "1 stale", "measured 3s ago",
		"lag 40", "SHARED cmd stream",
		"Wake", "woken", "nonce echoed", "not read or accepted", "tries 2", "escalating", "pane focused",
		"Gap", "possible, not proven",
		"Gate", "1 open",
		"Context", "608k", "608k ctx",
		"Board", "pr-46-review", "fix/pr-46",
		"Pilot", "holds the pilot lease",
		"in the retained window",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent detail lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "other") {
		t.Errorf("a task owned by someone else is listed:\n%s", body)
	}

	_, empty := agentDetail("ghost", &agentState{}, false, nil, nil, detNow)
	for _, want := range []string{"no status message", "never declared", "not recorded", "not armed",
		"not measured yet", "no wake record", "Gate", "none", "not reported", "none in the retained window"} {
		if !strings.Contains(empty, want) {
			t.Errorf("empty agent detail lacks %q:\n%s", want, empty)
		}
	}
	failed := &agentState{queueKnown: true, queueFailed: true, waiting: 3, queueAt: detNow.Add(-20 * time.Second), ctxRaw: "25%"}
	_, fb := agentDetail("x", failed, false, nil, nil, detNow)
	if !strings.Contains(fb, "read failed") || !strings.Contains(fb, "20s ago") || !strings.Contains(fb, "not a token count: 25%") {
		t.Errorf("failed queue / bad ctx:\n%s", fb)
	}
}

func TestAgentDetailRecentFromWindow(t *testing.T) {
	var feed []feedLine
	for i := 0; i < 8; i++ {
		feed = append(feed, feedLine{id: fmt.Sprint(i), at: detNow.Add(time.Duration(i) * time.Second),
			ev: bus.Event{Kind: "report", Agent: "grok", RKind: "note", Message: fmt.Sprintf("report %d", i)}})
	}
	feed = append(feed, feedLine{id: "x", at: detNow, ev: bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "grok", Message: "to grok"}},
		feedLine{id: "y", at: detNow, ev: bus.Event{Kind: "report", Agent: "kimi", Message: "not grok"}},
		feedLine{id: "s", sep: true, at: detNow})
	_, body := agentDetail("grok", richAgent(), false, nil, feed, detNow)
	if strings.Contains(body, "report 3") || !strings.Contains(body, "report 4") || !strings.Contains(body, "to grok") || strings.Contains(body, "not grok") {
		t.Fatalf("recent lines must be the last 5 involving grok:\n%s", body)
	}
	for i := 0; i < 8; i++ {
		if got, want := strings.Contains(body, fmt.Sprintf("report %d", i)), i >= 4; got != want {
			t.Fatalf("report %d shown=%v, want %v:\n%s", i, got, want, body)
		}
	}
}

func TestAgentDetailPlainIsRaw(t *testing.T) {
	a := richAgent()
	if agentDetailPlain(a) != a.message {
		t.Fatalf("plain %q", agentDetailPlain(a))
	}
}

func TestAgentDetailEscapesTags(t *testing.T) {
	a := &agentState{state: "idle", message: "[red]x[-] \x1b[31m\u202e"}
	_, body := agentDetail("a[b]", a, false, nil, nil, detNow)
	if !strings.Contains(body, "[red[]x[-[]") || strings.ContainsAny(body, "\x1b\u202e") {
		t.Fatalf("message not escaped/sanitised:\n%q", body)
	}
}

func TestTaskAndRequestDetail(t *testing.T) {
	e := bus.BoardEntry{Owner: "codex", State: "working", Branch: "v0.9/t1", Updated: detNow.Add(-2 * time.Minute).Unix()}
	title, body, plain := taskDetail(e, "ui-theme", detNow)
	for _, want := range []string{"ui-theme", "codex", "working", "v0.9/t1", "2m ago"} {
		if !strings.Contains(body, want) {
			t.Errorf("task detail lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(title, "ui-theme") || plain != "ui-theme codex working v0.9/t1" {
		t.Errorf("task title %q plain %q", title, plain)
	}
	_, nb, np := taskDetail(bus.BoardEntry{Owner: "x", State: "done"}, "t", detNow)
	if !strings.Contains(nb, GlyphDash) || np != "t x done "+GlyphDash {
		t.Errorf("task without branch: %q / %q", nb, np)
	}
	r := request{Task: "t-blocked", Tracked: true, From: "master", Target: "kimi", State: "blocked",
		BlockedReason: "need API key", Created: detNow.Add(-5 * time.Minute), Updated: detNow}
	rt, rb, rp := requestDetail(r, detNow)
	if !strings.Contains(rt, "t-blocked") || !strings.Contains(rb, "need API key") || !strings.Contains(rb, "no deadline set") {
		t.Errorf("request detail %q:\n%s", rt, rb)
	}
	if !strings.Contains(rp, "t-blocked") || !strings.Contains(rp, "need API key") || strings.Contains(rp, "[") {
		t.Errorf("request plain %q", rp)
	}
}

func TestMessageDetailOrder(t *testing.T) {
	e := bus.Event{ID: "1791649424805-0", Kind: "cmd", Type: "directive", From: "master", Target: "codex",
		Message: "BODYTEXT", TextComplete: "yes"}
	got := detailText(e, detNow, detNow, threadState{Loaded: true})
	body, from, id := strings.Index(got, "BODYTEXT"), strings.Index(got, "from"), strings.Index(got, "1791649424805-0")
	if body < 0 || from < 0 || id < 0 || !(body < from && from < id) {
		t.Fatalf("order body %d < from %d < id %d violated:\n%s", body, from, id, got)
	}
	if strings.Index(got, "complete") > from {
		t.Fatalf("fidelity must sit with the body, before the metadata:\n%s", got)
	}
}
