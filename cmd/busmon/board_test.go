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
	all := boardOpts{showDone: true}
	for w := 1; w <= 120; w++ {
		v := boardViewOf(m, boardNow, w, all)
		if h := v.header; h.Width() != w {
			t.Fatalf("header width %d at %d", h.Width(), w)
		}
		for _, it := range v.items {
			if it.Line.Width() != w {
				t.Fatalf("row %q width %d at %d", it.Key, it.Line.Width(), w)
			}
		}
	}
	its := boardViewOf(m, boardNow, 60, all).items
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
	v := boardViewOf(nil, boardNow, 40, boardOpts{})
	if its := v.items; len(its) != 1 || its[0].Selectable || !strings.Contains(its[0].Line.text(), "no task on the board") {
		t.Fatalf("empty board %+v", its)
	}
	if v.label.text() != "0 open "+GlyphNone+" 0 done (d)" {
		t.Fatalf("empty frame %q", v.label.text())
	}
	if l := boardViewOf(boardFixture(), boardNow, 40, boardOpts{}).label.text(); l != "4 open "+GlyphNone+" 1 done (d)" {
		t.Fatalf("frame label %q", l)
	}
	only := map[string]bus.BoardEntry{"release": {Owner: "master", State: "done"}} // done hidden: says how to see it
	if its := boardViewOf(only, boardNow, 60, boardOpts{}).items; len(its) != 1 || !strings.Contains(its[0].Line.text(), "no open task (d shows 1 done)") {
		t.Fatalf("all done %+v", its)
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

// The task column fits its longest task plus a gap (A2): on a wide screen
// owner and state follow the tasks instead of sitting at the far right.
func TestBoardTaskColumnFitsContent(t *testing.T) {
	m := map[string]bus.BoardEntry{
		"kimi-quota":        {Owner: "coder", State: "accepted", Updated: boardNow.Unix()},
		"grok-permission-x": {Owner: "architect", State: "done", Updated: boardNow.Unix()},
	}
	want := Width("grok-permission-x") + 2
	if got := boardTaskW(m, 200); got != want {
		t.Fatalf("task column %d at 200, want %d", got, want)
	}
	all := boardOpts{showDone: true}
	h := boardViewOf(m, boardNow, 200, all).header.text()
	if i := strings.Index(h, "OWNER"); i != want {
		t.Fatalf("owner at %d in %q, want %d", i, h, want)
	}
	for _, it := range boardViewOf(m, boardNow, 200, all).items {
		if it.Line.Width() != 200 {
			t.Fatalf("row %q width %d", it.Line.text(), it.Line.Width())
		}
		if !strings.HasPrefix(it.Line.text()[want:], m[it.Key].Owner) {
			t.Fatalf("owner not after the task column: %q", it.Line.text())
		}
	}
	// A narrow width still gives the task what the others leave.
	if got := boardTaskW(m, 30); got != 30-boardOwnerW-boardStateW-boardAgeW {
		t.Fatalf("task column %d at 30", got)
	}
	// An empty board keeps the header word.
	if got := boardTaskW(nil, 200); got != Width("TASK")+2 {
		t.Fatalf("empty board task column %d", got)
	}
}

// request-lost-body (foureyes): a request closed unaccepted (its body lost
// to retention, or expired) reads "closed … body missing", never plain done:
// nothing was delivered.
func TestBoardShowsAClosedUnacceptedRequest(t *testing.T) {
	m := map[string]bus.BoardEntry{"lost": {Owner: "coder", State: "done", Updated: boardNow.Unix(),
		Request: &bus.RequestInfo{ID: "1-0", Target: "coder", RespondedAt: boardNow.UnixMilli(), ResponseID: "2-0", ClosedUnaccepted: "body missing"}}}
	its := boardViewOf(m, boardNow, 100, boardOpts{showDone: true}).items
	if len(its) != 1 || !strings.Contains(its[0].Line.text(), "closed") || !strings.Contains(its[0].Line.text(), "body missing, nothing delivered") || strings.Contains(its[0].Line.text(), "done") {
		t.Fatalf("row: %q", its[0].Line.text())
	}
	reqs := boardRequests(m)
	if text, _ := nextAction(reqs[0], boardNow); !strings.Contains(text, "closed unaccepted: body missing") || !strings.Contains(text, "nothing delivered") {
		t.Fatalf("next action: %q", text)
	}
}
