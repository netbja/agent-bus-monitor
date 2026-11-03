package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/notify"
)

var nfNow = time.Date(2026, 11, 3, 14, 0, 0, 0, time.Local)

func nfMS(d time.Duration) int64 { return nfNow.Add(d).UnixMilli() }

func eventsByKey(evs []notify.Event) map[string]notify.Event {
	out := map[string]notify.Event{}
	for _, e := range evs {
		out[e.Key()] = e
	}
	return out
}

// Every fact the spec names makes its event, once; nothing a person wrote,
// no report, no warn in hand.
func TestNotifyEventsFromFacts(t *testing.T) {
	f := notifyFacts{
		Asks: []bus.Ask{
			{ID: "1793-0", From: "master", Title: "Merge PR 281?", State: "open", CreatedAt: nfMS(-time.Hour),
				Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1"}}, {AskQuestion: bus.AskQuestion{ID: "q2"}}}},
			{ID: "1792-0", From: "coder", Title: "closed", CreatedAt: nfMS(-2 * time.Hour), State: "answered"},
		},
		Agents: map[string]bus.AgentSnapshot{
			"coder":    {State: "waiting", Message: "pick a name", TS: nfMS(-10 * time.Minute)},
			"foureyes": {State: "idle", TS: nfMS(-time.Hour)},
			"sentinel": {State: "working", PID: 42, TS: nfMS(-time.Hour)},
			"docs":     {State: "waiting", PID: 43, TS: nfMS(-time.Hour)},
		},
		PlanWaiting: []notifyPlanTask{{ID: "plan-view-2", Title: "Merge it"}},
		Health: map[string]health.Record{
			"team-stall": {ID: "team-stall", Status: health.Fail, Since: nfMS(-20 * time.Minute), ObservedAt: nfMS(0), Detail: "stall-detail-x"},
			"disk":       {ID: "disk", Status: health.Fail, Since: nfMS(-time.Hour), ObservedAt: nfMS(0), Detail: "/ 1% free"},
			"backup":     {ID: "backup", Status: health.Warn, Since: nfMS(-time.Hour), ObservedAt: nfMS(0), Detail: "no archive"},
			"context":    {ID: "context", Status: health.Warn, Since: nfMS(-time.Hour), ObservedAt: nfMS(0)}, // in hand
			"github":     {ID: "github", Status: health.OK, Since: nfMS(-time.Hour), ObservedAt: nfMS(0)},
		},
		Notices: []bus.RawEntry{
			{ID: "1793710000000-0", Fields: map[string]string{"from": "dispatch", "message": "escalation nobody can act on", "for": "person"}},
			{ID: "1793710000001-0", Fields: map[string]string{"from": "dispatch", "message": "resume"}},
		},
		Observed: map[string]bus.Observation{
			"sentinel": {State: "blocked", PID: 42, Since: nfMS(-5 * time.Minute), At: nfMS(0)},
			"docs":     {State: "blocked", PID: 43, Since: nfMS(-5 * time.Minute), At: nfMS(0)}, // declares waiting: not a prompt
		},
		PromptGrace: 30 * time.Second,
	}
	got := eventsByKey(notifyEvents(f, map[string]time.Time{"plan-view-2": nfNow.Add(-3 * time.Minute)}, nfNow))
	want := []string{
		"decision|ask:1793-0",
		"decision|wait:coder|" + itoa64(nfMS(-10*time.Minute)),
		"decision|wait:docs|" + itoa64(nfMS(-time.Hour)),
		"decision|plan:plan-view-2",
		"stall|" + itoa64(nfMS(-20*time.Minute)),
		"fail|disk|" + itoa64(nfMS(-time.Hour)),
		"for-you|backup|" + itoa64(nfMS(-time.Hour)),
		"blocked|1793710000000-0",
		"prompt|sentinel|42:" + itoa64(nfMS(-5*time.Minute)),
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("no event %s in %v", k, keys(got))
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d events, want %d: %v", len(got), len(want), keys(got))
	}
	// team-stall is a stall only, never a second fail line.
	for k := range got {
		if strings.HasPrefix(k, "fail|team-stall") {
			t.Errorf("team-stall also as a fail: %s", k)
		}
	}
	a := got["decision|ask:1793-0"]
	if !a.At.Equal(time.UnixMilli(nfMS(-time.Hour))) || strings.Contains(a.Line, "Merge") || a.Detail != "Merge PR 281?" {
		t.Errorf("ask event %+v", a)
	}
	if !strings.Contains(a.Line, "master") || !strings.Contains(a.Line, "2 questions") {
		t.Errorf("ask line %q", a.Line)
	}
	if w := got["decision|wait:coder|"+itoa64(nfMS(-10*time.Minute))]; strings.Contains(w.Line, "pick a name") || w.Detail != "pick a name" {
		t.Errorf("wait event %+v", w)
	}
	if p := got["decision|plan:plan-view-2"]; !p.At.Equal(nfNow.Add(-3*time.Minute)) || strings.Contains(p.Line, "Merge it") {
		t.Errorf("plan event %+v", p)
	}
	for _, e := range got { // a summary line carries no free text from an agent
		for _, s := range []string{"pick a name", "Merge", "1% free", "escalation nobody", "no archive", "stall-detail-x"} {
			if strings.Contains(e.Line, s) {
				t.Errorf("%s line %q carries %q", e.Key(), e.Line, s)
			}
		}
	}
}

// A stale health record is unknown: no event.
func TestNotifyEventsSkipAStaleRecord(t *testing.T) {
	f := notifyFacts{Health: map[string]health.Record{
		"disk": {ID: "disk", Status: health.Fail, Since: nfMS(-48 * time.Hour), ObservedAt: nfMS(-24 * time.Hour)},
	}}
	if evs := notifyEvents(f, nil, nfNow); len(evs) != 0 {
		t.Fatalf("%v", evs)
	}
}

// A prompt is an event only past its grace, for the process registered,
// without an open ask of the agent's own.
func TestNotifyPromptRules(t *testing.T) {
	base := func() notifyFacts {
		return notifyFacts{
			Agents:      map[string]bus.AgentSnapshot{"coder": {State: "working", PID: 7}},
			Observed:    map[string]bus.Observation{"coder": {State: "blocked", PID: 7, Since: nfMS(-time.Minute), At: nfMS(0)}},
			PromptGrace: 30 * time.Second,
		}
	}
	if n := len(notifyEvents(base(), nil, nfNow)); n != 1 {
		t.Fatalf("a prompt: %d events", n)
	}
	young := base()
	young.Observed["coder"] = bus.Observation{State: "blocked", PID: 7, Since: nfMS(-10 * time.Second), At: nfMS(0)}
	other := base()
	other.Observed["coder"] = bus.Observation{State: "blocked", PID: 8, Since: nfMS(-time.Minute), At: nfMS(0)}
	asking := base()
	asking.Asks = []bus.Ask{{ID: "1-0", From: "coder", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q"}}}}}
	off := base()
	off.PromptGrace = 0
	for name, f := range map[string]notifyFacts{"young": young, "other pid": other, "asking": asking, "grace off": off} {
		for _, e := range notifyEvents(f, nil, nfNow) {
			if e.Kind == "prompt" {
				t.Errorf("%s: %+v", name, e)
			}
		}
	}
}

func keys(m map[string]notify.Event) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
