package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var boardNow = time.Date(2026, 10, 10, 18, 30, 0, 0, time.UTC)

func boardFixture() map[string]bus.BoardEntry {
	at := func(d time.Duration) int64 { return boardNow.Add(-d).Unix() }
	return map[string]bus.BoardEntry{
		"ui-theme":        {Owner: "codex", State: "working", Branch: "v0.9/t1", Updated: at(2 * time.Minute)},
		"dispatch-accept": {Owner: "master", State: "requested", Updated: at(14 * time.Minute)},
		"pr-46-review":    {Owner: "grok", State: "blocked", Updated: at(6 * time.Minute)},
		"release":         {Owner: "master", State: "done", Updated: at(time.Hour)},
		"日本語のタスク名がとても長い👩\u200d💻": {Owner: strings.Repeat("o", 32), State: "weird", Updated: at(time.Second)},
	}
}

func TestBoardItemsWidthAndRoles(t *testing.T) {
	m := boardFixture()
	for w := 1; w <= 120; w++ {
		if h := boardHeader(w); h.Width() != w {
			t.Fatalf("header width %d at %d", h.Width(), w)
		}
		for _, it := range boardItems(m, boardNow, w) {
			if it.Line.Width() != w {
				t.Fatalf("row %q width %d at %d", it.Key, it.Line.Width(), w)
			}
		}
	}
	its := boardItems(m, boardNow, 60)
	if its[1].Key != "ui-theme" || its[0].Key == "release" || its[len(its)-1].Key != "release" {
		t.Fatalf("order %v", keysOf(its))
	}
	roles := map[string]Role{"working": RoleOK, "requested": RoleAttention, "accepted": RoleAccent,
		"blocked": RoleError, "done": RoleDone, "weird": RoleMuted}
	for s, r := range roles {
		if got := boardStateRole(s); got != r {
			t.Errorf("state %s role %v, want %v", s, got, r)
		}
	}
	for _, it := range its {
		if !it.Selectable {
			t.Fatalf("task %q not selectable", it.Key)
		}
		if strings.Contains(it.Line.text(), "v0.9/t1") {
			t.Fatalf("branch belongs to the detail, not the row: %q", it.Line.text())
		}
	}
}

func keysOf(its []ListItem) []string {
	var out []string
	for _, it := range its {
		out = append(out, it.Key)
	}
	return out
}

func TestBoardEmpty(t *testing.T) {
	its := boardItems(nil, boardNow, 40)
	if len(its) != 1 || its[0].Selectable || !strings.Contains(its[0].Line.text(), "no task on the board") {
		t.Fatalf("empty board %+v", its)
	}
	title, label := boardFrame(nil)
	if title.text() != "Board" || label.text() != "0/0 done" {
		t.Fatalf("empty frame %q %q", title.text(), label.text())
	}
	title, label = boardFrame(boardFixture())
	if label.text() != "1/5 done" {
		t.Fatalf("frame label %q", label.text())
	}
}

func requestFixture() ([]request, []openThread) {
	reqs := []request{
		{Task: "t-blocked", Tracked: true, From: "master", Target: "kimi", State: "blocked", BlockedReason: "need API key",
			Created: boardNow.Add(-5 * time.Minute), Updated: boardNow.Add(-time.Minute)},
		{Task: "t-req", Tracked: true, From: "master", Target: "codex", State: "requested",
			Updated: boardNow.Add(-3 * time.Minute), Expires: boardNow.Add(10 * time.Minute)},
		{Task: "t-done", Tracked: true, From: "master", Target: "grok", State: "done", Updated: boardNow},
		{Task: "plain", Owner: "agy", State: "working"}, // a board task, not a request
	}
	threads := []openThread{{ID: "1791-0", Type: "directive", From: "master", Target: "agy",
		Message: "look at\tthis\nplease", At: boardNow.Add(-2 * time.Minute)}}
	return reqs, threads
}

func TestRequestSections(t *testing.T) {
	reqs, threads := requestFixture()
	its := requestItems(reqs, threads, boardNow, 50)
	var all []string
	for _, it := range its {
		if it.Line.Width() != 50 {
			t.Fatalf("item %q width %d", it.Key, it.Line.Width())
		}
		all = append(all, it.Line.text())
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"tracked requests", "untracked", "t-blocked", "need API key", "t-req", "no deadline", "deadline in 10m", "look at this please", "asked 2m ago"} {
		if want == "no deadline" {
			continue // t-blocked has no deadline: checked below
		}
		if !strings.Contains(text, want) {
			t.Errorf("requests lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "t-done") || strings.Contains(text, "plain") {
		t.Errorf("done request or plain task listed:\n%s", text)
	}
	if strings.Contains(strings.ToLower(text), "accepted") {
		t.Errorf("untracked thread must never read accepted:\n%s", text)
	}
	selectable := 0
	for _, it := range its {
		if it.Selectable {
			selectable++
			if !strings.HasPrefix(it.Key, "req:") && !strings.HasPrefix(it.Key, "thr:") {
				t.Errorf("selectable key %q", it.Key)
			}
		} else if it.Key != "" {
			t.Errorf("non-selectable item with key %q", it.Key)
		}
	}
	if selectable != waitingCount(reqs, threads) || selectable != 3 {
		t.Fatalf("selectable %d, waitingCount %d, want 3", selectable, waitingCount(reqs, threads))
	}
	blockedReasonRole := RoleText
	for _, it := range its {
		for _, s := range it.Line {
			if strings.Contains(s.Text, "need API key") {
				blockedReasonRole = s.Role
			}
		}
	}
	if blockedReasonRole != RoleError {
		t.Errorf("blocked reason role %v", blockedReasonRole)
	}
	if !strings.Contains(text, "no deadline") {
		t.Errorf("a request without Expires must say no deadline:\n%s", text)
	}
}

func TestRequestsEmpty(t *testing.T) {
	its := requestItems(nil, nil, boardNow, 40)
	if len(its) != 1 || its[0].Selectable || !strings.Contains(its[0].Line.text(), "nothing waiting") {
		t.Fatalf("empty requests %+v", its)
	}
}

func TestWaitingCountMatchesWaitingLine(t *testing.T) {
	reqs, threads := requestFixture()
	if got, line := waitingCount(reqs, threads), waitingText(reqs, threads, boardNow); line != "3 requests waiting · oldest 5m" || got != 3 {
		t.Fatalf("waitingCount %d vs waitingText %q", got, line)
	}
	if waitingLine(reqs, threads, boardNow) != tag("yellow", waitingText(reqs, threads, boardNow)) {
		t.Fatal("waitingLine must stay the tagged form of waitingText")
	}
	if waitingCount(nil, nil) != 0 {
		t.Fatal("empty count")
	}
}
