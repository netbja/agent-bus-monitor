package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// idleMasterWorld is the stall world with master_idle_after = 10m and the
// clock 12 minutes on (the merge queue's pass, written now in Redis time,
// still fresh), master's pane seen then.
func idleMasterWorld(t *testing.T) *stallWorld {
	t.Helper()
	w := newStallWorld(t)
	w.env["AGENT_BUS_MASTER_IDLE_AFTER"] = "10m"
	w.at(time.Now().Add(12 * time.Minute))
	return w
}

// at moves the clock and observes master's and coder's panes idle then.
func (w *stallWorld) at(now time.Time) {
	w.t.Helper()
	w.s.now = func() time.Time { return now }
	w.s.redisNow = func(context.Context) (time.Time, error) { return now, nil }
	seen := bus.Observation{State: "idle", At: now.UnixMilli()}
	if err := w.b.FencedPutObserved(context.Background(), "stall-holder", map[string]bus.Observation{"master": seen, "coder": seen}); err != nil {
		w.t.Fatal(err)
	}
}

// drain reads master's waiting messages, as a woken master does.
func (w *stallWorld) drain(agent string) {
	w.t.Helper()
	if _, err := w.b.DrainCmd(context.Background(), agent, agent, bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		w.t.Fatal(err)
	}
}

func (w *stallWorld) quotaMarks() map[string]string {
	w.t.Helper()
	m, err := w.b.QuotaMarks(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

// §3.1, §5: an approved head the merge queue lists as undelivered warns,
// and wakes master once, its time kept with the mark; with no pass read
// yet, the source is unknown, never ok.
func TestIdleMasterReadsTheMergeQueue(t *testing.T) {
	w := idleMasterWorld(t)
	ctx := context.Background()
	if out := w.run(w.s.idleMaster); out.Status != health.Unknown || out.Detail != "merge queue not read yet" {
		t.Fatalf("no pass: %+v", out)
	}
	head := "fa2d796f0123456789abcdef0123456789abcdef"
	if err := w.b.FencedPutMergeQueue(ctx, "stall-holder", bus.MergeQueueRecord{Source: "git", Forge: "none", Base: "main",
		Undelivered: []bus.MergeUndelivered{{Subject: "herdr-6c2-2", Head: head}}}); err != nil {
		t.Fatal(err)
	}
	out := w.run(w.s.idleMaster)
	if out.Status != health.Warn || out.Detail != "master idle 12m; approved, not pushed: herdr-6c2-2 (fa2d796f)" {
		t.Fatalf("undelivered: %+v", out)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
			t.Fatal(err)
		}
	}
	got := w.dispatchDirectives("master")
	if len(got) != 1 || !strings.HasPrefix(got[0], "Stall guard: master idle 12m with work in hand: approved, not pushed: herdr-6c2-2 (fa2d796f).") {
		t.Fatalf("master woken %q", got)
	}
	if m := w.quotaMarks(); m[health.StallMasterMark] == "" || bus.MarkAt(m, health.StallMasterMark) == 0 {
		t.Fatalf("marks %v: want the wake and its time", m)
	}
	if n := w.personNotices(); len(n) != 0 {
		t.Fatalf("the person told at once: %q", n)
	}
	if out := w.run(w.s.idleMaster); out.Status != health.OK || out.Detail != "master has messages; the dispatcher wakes it" {
		t.Fatalf("the wake unread: %+v", out)
	}
	w.drain("master")
	// The head delivered: nothing in master's hand.
	if err := w.b.FencedPutMergeQueue(ctx, "stall-holder", bus.MergeQueueRecord{Source: "git", Forge: "none", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleMaster); out.Status != health.OK || out.Detail != "nothing in master's hand" {
		t.Fatalf("delivered: %+v", out)
	}
	w.env["AGENT_BUS_MASTER_IDLE_AFTER"] = "0"
	if out := w.run(w.s.idleMaster); out.Status != health.Unknown || out.Detail != "off (master_idle_after = 0)" {
		t.Fatalf("off: %+v", out)
	}
}

// §2, §5: a request for master, read, warns (the stale merge queue named);
// unread, the dispatcher's own wake covers it. Woken, idle again as long on
// the same items: the person is told once.
func TestIdleMasterSecondTierLive(t *testing.T) {
	w := idleMasterWorld(t)
	ctx := context.Background()
	if _, err := w.b.Request(ctx, "release-v0-30", "coder", "master", "", "cut the release", 0); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleMaster); out.Status != health.OK || out.Detail != "master has messages; the dispatcher wakes it" {
		t.Fatalf("unread: %+v", out)
	}
	w.drain("master")
	out := w.run(w.s.idleMaster)
	if out.Status != health.Warn || out.Detail != "master idle 12m; requested: release-v0-30; merge queue not read yet" {
		t.Fatalf("requested: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
		t.Fatal(err)
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 {
		t.Fatalf("directives to master %q", got)
	}
	w.drain("master")
	w.at(time.Now().Add(23 * time.Minute)) // the wake's :at is Redis time (now): woken 23m ago
	w.run(w.s.idleMaster)
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
			t.Fatal(err)
		}
	}
	n := w.personNotices()
	if len(n) != 1 || !strings.HasPrefix(n[0], "Stall guard: master was woken 23m ago for: requested: release-v0-30.") {
		t.Fatalf("person told %q", n)
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 {
		t.Fatalf("master woken again: %q", got)
	}
}

// §4: master's check-back due is an item; set a fourth time on the same
// what, the person is told once, whether idle-master warns or not.
func TestIdleMasterCheckback(t *testing.T) {
	w := idleMasterWorld(t)
	ctx := context.Background()
	if err := w.b.FencedPutMergeQueue(ctx, "stall-holder", bus.MergeQueueRecord{Source: "git", Forge: "none", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := w.b.SetCheckback(ctx, "master", 5*time.Minute, "k1/k2 checks"); err != nil {
			t.Fatal(err)
		}
	}
	w.at(time.Now().Add(12 * time.Minute)) // after the sets: due 7m ago, whole minutes
	out := w.run(w.s.idleMaster)
	if out.Status != health.Warn || out.Detail != "master idle 12m; check-back due 7m ago: k1/k2 checks" {
		t.Fatalf("due: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
		t.Fatal(err)
	}
	if n := w.personNotices(); len(n) != 0 {
		t.Fatalf("told at the third: %q", n)
	}
	if _, err := w.b.SetCheckback(ctx, "master", time.Hour, "K1/K2 checks "); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleMaster); out.Status != health.OK {
		t.Fatalf("not due: %+v", out)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.personNotices(); len(n) != 1 || !strings.Contains(n[0], `master has postponed "K1/K2 checks" 4 times since`) {
		t.Fatalf("person told %q", n)
	}
}

// §5 (reviewer-codex): idle-master judges by the broker's clock, never the
// dispatcher host's: a host clock two hours ahead does not make master,
// idle since now in Redis time, idle for two hours.
func TestIdleMasterUsesRedisTime(t *testing.T) {
	w := idleMasterWorld(t)
	w.s.redisNow = nil
	ahead := time.Now().Add(2 * time.Hour)
	w.s.now = func() time.Time { return ahead }
	seen := bus.Observation{State: "idle", At: time.Now().UnixMilli()}
	if err := w.b.FencedPutObserved(context.Background(), "stall-holder", map[string]bus.Observation{"master": seen}); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleMaster); out.Status != health.OK || out.Detail != "master idle 0m" {
		t.Fatalf("host clock ahead: %+v, want master idle 0m by Redis time", out)
	}
}
