package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// report-one-row §5 B: agentbus reports says, per notice, whether the person
// opened it ({p}:seen) or whether the agent's group delivered it.

func notice(id, report, target string) bus.Event {
	return bus.Event{Kind: "cmd", ID: id, From: "master", Target: target, Type: bus.CmdDirective, Ref: report,
		Message: reportNotice(report, "## Done\nfine")}
}

func TestNoticesByReportNeedTheExactRef(t *testing.T) {
	forged := notice("3-0", "1-0", "bob")
	forged.Ref = "9-0"
	byReport := noticesByReport([]bus.Event{notice("2-0", "1-0", "bob"), forged,
		{Kind: "cmd", ID: "4-0", Type: bus.CmdDirective, Ref: "1-0", Message: "plain directive"}})
	if got := byReport["1-0"]; len(got) != 1 || got[0].ID != "2-0" {
		t.Fatalf("notices of 1-0: %+v", got)
	}
}

func TestReportsTableMarksReadState(t *testing.T) {
	at := time.Date(2026, 11, 5, 7, 31, 0, 0, time.Local)
	evs := []bus.Event{
		{ID: "1-0", Agent: "master", RKind: "note", Message: "a", TextComplete: "yes"},
		{ID: "5-0", Agent: "master", RKind: "note", Message: "b", TextComplete: "yes"},
		{ID: "8-0", Agent: "coder", RKind: "note", Message: "c", TextComplete: "yes"},
	}
	states := map[string][]noticeState{
		"1-0": {{Target: "bob", ID: "2-0", Person: true, SeenAt: at}, {Target: "coder", ID: "3-0", Delivered: 1}},
		"5-0": {{Target: "bob", ID: "6-0", Person: true}},
	}
	rows := strings.Split(strings.TrimRight(reportsTableWith(evs, states), "\n"), "\n")
	if !strings.HasSuffix(rows[0], "→ bob read 07:31, → coder delivered") {
		t.Fatalf("row 1: %q", rows[0])
	}
	if !strings.HasSuffix(rows[1], "→ bob unread") {
		t.Fatalf("row 2: %q", rows[1])
	}
	if strings.Contains(rows[2], "→") {
		t.Fatalf("a report with no notice has no mark: %q", rows[2])
	}
	if got := unreadReports(evs, states, ""); len(got) != 1 || got[0].ID != "5-0" {
		t.Fatalf("--unread: %+v", got)
	}
	if got := unreadReports(evs, states, "alice"); len(got) != 0 {
		t.Fatalf("--unread --to alice: %+v", got)
	}
	if got := unreadReports(evs, states, "bob"); len(got) != 1 {
		t.Fatalf("--unread --to bob: %+v", got)
	}
}

func TestReportsJSONCarriesNotices(t *testing.T) {
	at := time.UnixMilli(1793900000000)
	out := reportsJSON([]bus.Event{{ID: "1-0", Agent: "master", Message: "a"}},
		map[string][]noticeState{"1-0": {{Target: "bob", ID: "2-0", Person: true, SeenAt: at}, {Target: "carol", ID: "3-0", Person: true}}})
	var got []struct {
		ID      string `json:"id"`
		Notices []struct {
			Target string  `json:"target"`
			ID     string  `json:"id"`
			SeenAt *string `json:"seen_at"`
		} `json:"notices"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err, string(out))
	}
	if len(got) != 1 || got[0].ID != "1-0" || len(got[0].Notices) != 2 {
		t.Fatalf("%s", out)
	}
	if n := got[0].Notices[0]; n.Target != "bob" || n.SeenAt == nil || *n.SeenAt != at.Format(time.RFC3339Nano) {
		t.Fatalf("seen notice: %+v", n)
	}
	if got[0].Notices[1].SeenAt != nil {
		t.Fatal("an unseen notice has a null seen_at")
	}
}

type fakeNoticeReader struct {
	cmds    []bus.Event
	agents  map[string]bus.AgentSnapshot
	seen    map[string]bus.Seen
	seenErr error
	last    map[string]string
}

func (f fakeNoticeReader) Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error) {
	return f.cmds, nil, nil
}
func (f fakeNoticeReader) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	return f.agents, nil
}
func (f fakeNoticeReader) Seen(context.Context) (map[string]bus.Seen, error) { return f.seen, f.seenErr }
func (f fakeNoticeReader) Obligation(_ context.Context, a string, _ time.Duration) (bus.Obligation, error) {
	l, ok := f.last[a]
	return bus.Obligation{GroupExists: ok, LastDelivered: l}, nil
}

func TestReadNoticeStates(t *testing.T) {
	at := time.UnixMilli(1793900000000)
	r := fakeNoticeReader{
		cmds:   []bus.Event{notice("4-0", "1-0", "coder"), notice("2-0", "1-0", "bob"), notice("3-0", "1-0", "reviewer")},
		agents: map[string]bus.AgentSnapshot{"coder": {}, "reviewer": {}},
		seen:   map[string]bus.Seen{"2-0": {At: at, By: "bob"}},
		last:   map[string]string{"coder": "10-0", "reviewer": "3-5"},
	}
	evs := []bus.Event{{ID: "1-0", Kind: "report"}}
	states, seenErr, err := readNoticeStates(context.Background(), r, evs)
	if err != nil || seenErr != nil {
		t.Fatal(err, seenErr)
	}
	ns := states["1-0"]
	if len(ns) != 3 || ns[0].ID != "2-0" || !ns[0].Person || !ns[0].SeenAt.Equal(at) {
		t.Fatalf("person notice first, read: %+v", ns)
	}
	if ns[1].Target != "reviewer" || ns[1].Person || ns[1].Delivered != 1 {
		t.Fatalf("an agent's notice under its group's cursor is delivered: %+v", ns[1])
	}
	if ns[2].Target != "coder" || ns[2].Delivered != 1 {
		t.Fatalf("%+v", ns[2])
	}
	r.last["coder"] = "3-9"
	states, _, _ = readNoticeStates(context.Background(), r, evs)
	if states["1-0"][2].Delivered != 0 {
		t.Fatalf("past the cursor is pending: %+v", states["1-0"][2])
	}
	r.seenErr = errors.New("down")
	states, seenErr, _ = readNoticeStates(context.Background(), r, evs)
	if seenErr == nil || !states["1-0"][0].Unknown || noticeMark(states["1-0"][0]) != "→ bob ?" {
		t.Fatalf("unreadable receipts are unknown: %+v", states["1-0"][0])
	}
}
