package mission

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func (h *harness) op(name string, args ...string) (bool, string) {
	h.t.Helper()
	return h.c.HandleOp(h.ctx, bus.MissionOp{Op: name, From: "master", Args: args})
}

func TestSkipUnlocksDependents(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.setState("t1", Blocked, nil)
	if ok, msg := h.op("skip", "chg/t1", "done by hand"); !ok {
		t.Fatalf("skip: %q", msg)
	}
	t1 := h.mission("t1")
	if t1.State != Integrated || !strings.Contains(t1.Reason, "skipped by master: done by hand") {
		t.Fatalf("t1: %+v", t1)
	}
	vs, _ := h.b.Verdicts(h.ctx, "mission:chg/t1@skipped")
	if len(vs) != 1 || vs[0].Author != "master" || vs[0].Reviewer != "master" || vs[0].Message != "done by hand" {
		t.Fatalf("ledger: %+v", vs)
	}
	h.pass()
	if t2 := h.mission("t2"); t2.State != Requested {
		t.Fatalf("t2: %s", t2.State)
	}
}

func TestSkipRefusedDuringMerge(t *testing.T) {
	for _, s := range []State{Approved, Integrating} {
		h := newHarness(t, "true")
		h.setState("t1", s, nil)
		if ok, msg := h.op("skip", "chg/t1", "x"); ok {
			t.Fatalf("%s: skip accepted: %q", s, msg)
		}
	}
}

func TestSkipInReviewClosesGate(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	if ok, msg := h.op("skip", "chg/t1", "not needed"); !ok {
		t.Fatalf("skip: %q", msg)
	}
	if open, _ := h.b.OpenChallenges(h.ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate left open: %v", open)
	}
}

func TestReassignBlocked(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.b.BlockRequest(h.ctx, "chg-t1-1", "kimi", "stuck")
	h.pass()
	if ok, msg := h.op("reassign", "chg/t1", "grok"); !ok {
		t.Fatalf("reassign: %q", msg)
	}
	t1 := h.mission("t1")
	board, _ := h.b.Board(h.ctx)
	if t1.State != Requested || t1.Current.Agent != "grok" || t1.Attempt != 2 || board["chg-t1-2"].Request.Target != "grok" {
		t.Fatalf("t1: %+v", t1)
	}
	if board["chg-t1-1"].State != "superseded" {
		t.Fatalf("old task: %q", board["chg-t1-1"].State)
	}
	// a later retry stays with the new agent
	h.b.AcceptRequest(h.ctx, "chg-t1-2", "grok")
	h.b.BlockRequest(h.ctx, "chg-t1-2", "grok", "stuck too")
	h.pass()
	h.op("retry", "chg/t1")
	if t1 := h.mission("t1"); t1.Current.Agent != "grok" {
		t.Fatalf("retry went back to %s", t1.Current.Agent)
	}
}

func TestReassignRefusals(t *testing.T) {
	h := newHarness(t, "true")
	h.pass() // t1 requested, not accepted
	if ok, _ := h.op("reassign", "chg/t1", "grok"); ok {
		t.Fatal("reassign of a requested mission")
	}
	h.setState("t1", Blocked, nil)
	before := h.snapshot()
	for _, agent := range []string{"codex", "kimi", "Bad Name"} {
		if ok, msg := h.op("reassign", "chg/t1", agent); ok {
			t.Fatalf("reassign to %s accepted: %q", agent, msg)
		}
	}
	if h.snapshot() != before {
		t.Fatal("a refused reassign wrote something")
	}
}
