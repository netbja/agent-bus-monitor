package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// stale-waiting §5: an answered wait raises no wait: event and no longer
// silences a prompt event; an unanswered one still does both.
func TestNotifyAnsweredWait(t *testing.T) {
	decl := nfMS(-time.Hour)
	answered := []bus.Ask{{ID: "1-0", From: "coder", State: "answered", Questions: []bus.AskQuestionState{
		{Answer: &bus.AskAnswer{By: "charlie", At: decl + 1000}}}}}
	facts := func(asks []bus.Ask) notifyFacts {
		return notifyFacts{
			Asks:        asks,
			Agents:      map[string]bus.AgentSnapshot{"coder": {State: "waiting", TS: decl, PID: 7}},
			Observed:    map[string]bus.Observation{"coder": {State: "blocked", PID: 7, Since: nfMS(-time.Minute), At: nfMS(0)}},
			PromptGrace: 30 * time.Second,
		}
	}
	kinds := func(f notifyFacts) map[string]bool {
		out := map[string]bool{}
		for _, e := range notifyEvents(f, nil, nfNow) {
			out[e.Kind+":"+e.Episode[:4]] = true
		}
		return out
	}
	if k := kinds(facts(nil)); !k["decision:wait"] || k["prompt:code"] {
		t.Fatalf("unanswered: %v, want the wait and no prompt", k)
	}
	if k := kinds(facts(answered)); k["decision:wait"] || !k["prompt:code"] {
		t.Fatalf("answered: %v, want no wait and the prompt", k)
	}
	human := facts(nil)
	human.Cmds = []bus.Event{{Kind: "cmd", ID: strconv.FormatInt(decl+5, 10) + "-0", From: "charlie", Target: "coder", Human: true}}
	if k := kinds(human); k["decision:wait"] {
		t.Fatalf("answered by a message: %v", k)
	}
}

// The cmd stream is read only when an agent is declared waiting; its failure
// then fails the pass's read.
func TestNotifyReadsCmdOnlyWhenSomeoneWaits(t *testing.T) {
	b := notifyBus(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	reads := 0
	r := &notifyReader{b: b, cmds: func(context.Context) ([]bus.Event, error) { reads++; return nil, errors.New("down") }}
	if _, failed := r.read(ctx, time.Now()); reads != 0 || failed["cmd"] != "" {
		t.Fatalf("nobody waiting: %d reads, failed %v", reads, failed)
	}
	if _, err := b.Status(ctx, "coder", "waiting", "q", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, failed := r.read(ctx, time.Now()); reads != 1 || failed["cmd"] == "" {
		t.Fatalf("coder waiting: %d reads, failed %v", reads, failed)
	}
}
