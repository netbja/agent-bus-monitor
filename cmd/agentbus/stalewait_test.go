package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// stale-waiting §4: answerWaits rewrites a waiting the person answered as
// idle since the answer; it reads the asks and cmd only when someone waits,
// and a failed read then makes the pass unknown.
func TestAnswerWaits(t *testing.T) {
	const decl = int64(1_000_000)
	team := func(masterState string, dormant bool) teamRead {
		return teamRead{
			snaps: map[string]bus.AgentSnapshot{"master": {State: masterState, TS: decl}, "coder": {State: "idle", TS: decl}},
			agents: []health.StallAgent{
				{Name: "master", State: masterState, DeclaredAt: time.UnixMilli(decl), Dormant: dormant},
				{Name: "coder", State: "idle", DeclaredAt: time.UnixMilli(decl)},
			},
		}
	}
	answeredAsk := []bus.Ask{{ID: "1-0", From: "master", State: "answered", Questions: []bus.AskQuestionState{
		{Answer: &bus.AskAnswer{By: "charlie", At: decl + 500}}}}}
	fail := errors.New("down")
	reads := 0
	asks := func(a []bus.Ask, err error) func() ([]bus.Ask, error) {
		return func() ([]bus.Ask, error) { reads++; return a, err }
	}
	cmds := func(err error) func() ([]bus.Event, error) {
		return func() ([]bus.Event, error) { reads++; return nil, err }
	}

	tr := team("waiting", false)
	if phrase := answerWaits(&tr, asks(answeredAsk, nil), cmds(nil)); phrase != "" {
		t.Fatalf("phrase %q", phrase)
	}
	if a := tr.agents[0]; a.State != "idle" || !a.AnsweredWait || a.DeclaredAt.UnixMilli() != decl+500 {
		t.Fatalf("answered wait read as %+v, want idle since the answer", a)
	}

	tr = team("waiting", false)
	if answerWaits(&tr, asks(nil, nil), cmds(nil)); tr.agents[0].State != "waiting" || tr.agents[0].AnsweredWait {
		t.Fatalf("no proof, read as %+v", tr.agents[0])
	}

	for _, c := range []struct {
		name string
		tr   teamRead
	}{{"nobody waiting", team("idle", false)}, {"only a dormant agent waiting", team("waiting", true)}} {
		reads = 0
		tr := c.tr
		if phrase := answerWaits(&tr, asks(nil, fail), cmds(fail)); phrase != "" || reads != 0 {
			t.Fatalf("%s: phrase %q after %d reads, want no read", c.name, phrase, reads)
		}
	}

	tr = team("waiting", false)
	if phrase := answerWaits(&tr, asks(nil, fail), cmds(nil)); phrase != "asks unavailable" {
		t.Fatalf("asks failed: %q", phrase)
	}
	tr = team("waiting", false)
	if phrase := answerWaits(&tr, asks(answeredAsk, nil), cmds(fail)); phrase != "cmd stream unavailable" || tr.agents[0].AnsweredWait {
		t.Fatalf("cmd failed: %q %+v", phrase, tr.agents[0])
	}
}

// The regression (2026-11-05): master asked, the person answered, master never
// lifted its waiting. team-stall must reach the plan and review-queue its
// gates, never settle on "master waiting".
func TestAnsweredWaitNeverBlocksTheGuards(t *testing.T) {
	w := newStallWorld(t)
	w.env["AGENT_BUS_QUIET_AFTER"] = "0"
	ctx := context.Background()
	id, _, err := w.b.Ask(ctx, bus.AskDef{From: "master", Title: "Merge?", Questions: []bus.AskQuestion{{
		ID: "q1", Header: "Merge", Question: "Merge PR 1?", Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Status(ctx, "master", "waiting", "1 question(s): Merge?", bus.AgentIdent{PID: os.Getpid(), Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	// The ask's own question is open: master waits, rightly.
	if out := w.run(w.s.teamStall); out.Status != health.OK || out.Detail != "master waiting" {
		t.Fatalf("open ask: %+v", out)
	}
	time.Sleep(2 * time.Millisecond) // the answer strictly after the declaration
	if _, _, err := w.b.Answer(ctx, id, "q1", "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	// master reads the answer (its wake) and declares nothing: the incident.
	if _, err := w.b.DrainCmd(ctx, "master", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	out := w.run(w.s.teamStall)
	if out.Status != health.Fail || out.Reason != "stalled" || !strings.Contains(out.Detail, "master's wait answered") {
		t.Fatalf("answered wait still blocks team-stall: %+v", out)
	}
	if out := w.run(w.s.reviewQueue); strings.Contains(out.Detail, "master waiting") {
		t.Fatalf("answered wait still blocks review-queue: %+v", out)
	}
	// master asks again: waiting again.
	if _, err := w.b.Status(ctx, "master", "waiting", "again", bus.AgentIdent{PID: os.Getpid(), Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.teamStall); out.Status != health.OK || out.Detail != "master waiting" {
		t.Fatalf("a newer waiting: %+v", out)
	}
}
