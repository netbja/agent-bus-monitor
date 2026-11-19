package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
	"github.com/redis/go-redis/v9"
)

// runWorld is a project with the lease, a run started, and the settings
// a test sets.
type runWorld struct {
	b   *bus.Bus
	r   *redis.Client
	ctx context.Context
	env map[string]string
	c   *mergeQueueChore
}

func newRunWorld(t *testing.T) *runWorld {
	t.Helper()
	b, r := bustest.Open(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	w := &runWorld{b: b, r: r, ctx: ctx, env: map[string]string{}}
	w.c = &mergeQueueChore{b: b, getenv: func(k string) string { return w.env[k] }}
	w.pass(t)
	return w
}

// pass is one merge-queue pass's run step: the run's write, then the
// limits acted on, with the undelivered heads given.
func (w *runWorld) pass(t *testing.T, undelivered ...bus.MergeUndelivered) bus.RunRecord {
	t.Helper()
	rec, _, err := w.b.FencedRunPass(w.ctx, "tok", nil, true, 3*mergeQueueEvery)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.c.runStep(w.ctx, "tok", rec, undelivered); err != nil {
		t.Fatal(err)
	}
	now, _ := w.b.Run(w.ctx)
	if now == nil {
		return bus.RunRecord{}
	}
	return *now
}

func (w *runWorld) count(t *testing.T, kind, target, text string) int {
	t.Helper()
	evs, _, err := w.b.Recent(w.ctx, []string{kind}, 200)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if (target == "" || e.Target == target) && strings.Contains(e.Message, text) {
			n++
		}
	}
	return n
}

// age moves the run's start back, as if it had run d longer.
func (w *runWorld) age(t *testing.T, field string, d time.Duration) {
	t.Helper()
	rec, _ := w.b.Run(w.ctx)
	v := map[string]int64{"started_at": rec.StartedAt, "since": rec.Since}[field]
	if err := w.r.HSet(w.ctx, bus.RunKey(w.b.Project()), field, v-d.Milliseconds()).Err(); err != nil {
		t.Fatal(err)
	}
}

// run-budget §3: no limit set, nothing happens; max_duration reached drains
// the run once: one directive to master, one notice for the person (their
// own marks: both sent), neither again on the next pass.
func TestRunStepDrainsOnceAtALimit(t *testing.T) {
	w := newRunWorld(t)
	w.age(t, "started_at", 10*time.Hour)
	if rec := w.pass(t); rec.State != bus.RunRunning {
		t.Fatalf("no limit set: %+v", rec)
	}
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "3h"
	w.env["AGENT_BUS_RUN_DRAIN_MAX"] = "0"
	// work in flight, so the drain does not stop at once
	if _, err := w.b.Status(w.ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	rec := w.pass(t)
	if rec.State != bus.RunDraining || rec.Why != bus.RunWhyDuration || rec.Drains != 1 {
		t.Fatalf("after the limit: %+v", rec)
	}
	w.pass(t)
	if n := w.count(t, "cmd", "master", "Run budget reached"); n != 1 {
		t.Fatalf("%d directives to master, want 1", n)
	}
	if n := w.count(t, "notify", "", "run budget reached"); n != 1 {
		t.Fatalf("%d person notices, want 1", n)
	}
}

// §3: max_deliveries counts deliveries; 0 never limits.
func TestRunStepDeliveriesLimit(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DELIVERIES"] = "1"
	if _, err := w.b.Status(w.ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunRunning {
		t.Fatalf("0 deliveries of 1: %+v", rec)
	}
	head := strings.Repeat("e", 40)
	rec, _, err := w.b.FencedRunPass(w.ctx, "tok", []string{head}, true, 3*mergeQueueEvery)
	if err != nil || rec.Deliveries != 1 {
		t.Fatalf("delivery %+v %v", rec, err)
	}
	if err := w.c.runStep(w.ctx, "tok", rec, nil); err != nil {
		t.Fatal(err)
	}
	if now, _ := w.b.Run(w.ctx); now.State != bus.RunDraining || now.Why != bus.RunWhyDeliveries {
		t.Fatalf("1 of 1: %+v", now)
	}
}

// §3: a draining run stops when nothing is in flight: a kind-run pause and
// one stop notice; an undelivered head, an accepted task, an open gate or a
// working agent keep it draining.
func TestRunStepStopsWhenNothingIsInFlight(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	w.age(t, "started_at", 2*time.Hour)
	if _, err := w.b.Status(w.ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("not draining: %+v", rec)
	}
	if _, err := w.b.Status(w.ctx, "coder", "idle", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t, bus.MergeUndelivered{Subject: "s", Head: strings.Repeat("f", 40)}); rec.State != bus.RunDraining {
		t.Fatalf("an undelivered head stopped the run: %+v", rec)
	}
	if _, err := w.b.RequestBy(w.ctx, "wiki-3", "charlie", "coder", "", "do it", 0, true); err != nil { // the person: the run drains
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("a requested task stopped the run: %+v", rec)
	}
	if err := w.b.BoardDrop(w.ctx, "wiki-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Status(w.ctx, "foureyes", "idle", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := w.b.OpenChallenge(w.ctx, "foureyes", "g1", "subject x"); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("an open gate stopped the run: %+v", rec)
	}
	if err := w.b.ResolveChallenge(w.ctx, "foureyes", "g1"); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunStopped {
		t.Fatalf("nothing in flight: %+v", rec)
	}
	if p, _ := w.b.Pause(w.ctx); p == nil || p.Kind != bus.PauseKindRun {
		t.Fatalf("pause %+v", p)
	}
	w.pass(t)
	if n := w.count(t, "notify", "", "run stopped"); n != 1 {
		t.Fatalf("%d stop notices, want 1", n)
	}
}

// §3: drain_max after the drain began stops the run with work still in
// flight, and the notice lists it.
func TestRunStepDrainMax(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	w.env["AGENT_BUS_RUN_DRAIN_MAX"] = "2h"
	w.age(t, "started_at", 2*time.Hour)
	if _, err := w.b.Status(w.ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("not draining: %+v", rec)
	}
	w.age(t, "since", 3*time.Hour)
	if rec := w.pass(t); rec.State != bus.RunStopped {
		t.Fatalf("drain_max passed: %+v", rec)
	}
	if n := w.count(t, "notify", "", "coder working"); n != 1 {
		t.Fatalf("the stop notice does not list the work in flight")
	}
}

// §4 with §3: a drain for a quota never stops the run (the quota's own
// pause holds the project until its release).
func TestRunStepQuotaDrainDoesNotStop(t *testing.T) {
	w := newRunWorld(t)
	if _, _, err := w.b.FencedRunDrain(w.ctx, "tok", bus.RunWhyQuota("anthropic")); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("a quota drain stopped: %+v", rec)
	}
}

// reviewer-codex: a gate on a reviewer with no status snapshot (offline)
// is in flight too.
func TestRunStepGateOnAnOfflineReviewer(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	w.age(t, "started_at", 2*time.Hour)
	if err := w.b.OpenChallenge(w.ctx, "security", "g1", "subject x"); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunDraining {
		t.Fatalf("an offline reviewer's gate stopped the run: %+v", rec)
	}
	if err := w.b.ResolveChallenge(w.ctx, "security", "g1"); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunStopped {
		t.Fatalf("nothing in flight: %+v", rec)
	}
}

// reviewer-codex: a transition committed whose message was not written
// (the write failed after it) is told on a later pass, once.
func TestRunStepRetriesMissingMessages(t *testing.T) {
	w := newRunWorld(t)
	if _, err := w.b.Status(w.ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	// drained, as if the pass that did it failed before its messages
	if _, _, err := w.b.FencedRunDrain(w.ctx, "tok", bus.RunWhyDeliveries); err != nil {
		t.Fatal(err)
	}
	w.pass(t)
	w.pass(t)
	if n := w.count(t, "cmd", "master", "Run budget reached"); n != 1 {
		t.Fatalf("%d drain directives after the retry, want 1", n)
	}
	if n := w.count(t, "notify", "", "run budget reached"); n != 1 {
		t.Fatalf("%d drain notices after the retry, want 1", n)
	}
	// stopped, as if the pass that did it failed before its notice
	if _, err := w.b.FencedRunStop(w.ctx, "tok", "master"); err != nil {
		t.Fatal(err)
	}
	w.pass(t)
	w.pass(t)
	if n := w.count(t, "notify", "", "run stopped"); n != 1 {
		t.Fatalf("%d stop notices after the retry, want 1", n)
	}
}

// foureyes: a pass that could not read the queue stops nothing (what is in
// flight is not known), however empty everything else is.
func TestRunStepUnknownQueueStopsNothing(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	w.age(t, "started_at", 2*time.Hour)
	rec, _, err := w.b.FencedRunPass(w.ctx, "tok", nil, false, 3*mergeQueueEvery)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.c.runStepKnown(w.ctx, "tok", rec, nil, false); err != nil {
		t.Fatal(err)
	}
	if now, _ := w.b.Run(w.ctx); now.State != bus.RunDraining {
		t.Fatalf("unknown queue: %+v, want draining (not stopped)", now)
	}
	if rec := w.pass(t); rec.State != bus.RunStopped {
		t.Fatalf("the queue known and empty: %+v", rec)
	}
}

// foureyes: §3's in flight is a board entry requested or accepted; a plan
// claim (board state working) does not hold the drain.
func TestRunStepBoardClaimIsNotInFlight(t *testing.T) {
	w := newRunWorld(t)
	w.env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	w.age(t, "started_at", 2*time.Hour)
	if err := w.b.BoardClaim(w.ctx, "plan-3", "coder", ""); err != nil {
		t.Fatal(err)
	}
	if err := w.b.BoardState(w.ctx, "plan-3", "working"); err != nil {
		t.Fatal(err)
	}
	if rec := w.pass(t); rec.State != bus.RunStopped {
		t.Fatalf("a board claim held the drain: %+v", rec)
	}
}
