package mission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reload commits a new missions.json and loads it (force reason "" = no force).
func (h *harness) reload(data, force string) (bool, string) {
	h.t.Helper()
	var commit string
	if cur, _ := os.ReadFile(filepath.Join(h.repo, "docs/changes/chg/missions.json")); string(cur) == data {
		commit = run(h.t, h.repo, "git", "rev-parse", "HEAD")
	} else {
		commit = commitFile(h.t, h.repo, "docs/changes/chg/missions.json", data, "revise")
	}
	args := []string{"docs/changes/chg/missions.json@" + commit}
	if force != "" {
		args = append(args, "force", force)
	}
	return h.c.HandleOp(h.ctx, bus.MissionOp{Op: "load", From: "master", Args: args})
}

// snapshot is the raw change and missions hashes, to prove nothing was written.
func (h *harness) snapshot() string {
	h.t.Helper()
	ch, _ := h.b.ReadMissionHash(h.ctx, bus.ChangeKey(h.b.Project(), "chg"))
	ms, _ := h.b.ReadMissionHash(h.ctx, bus.MissionsKey(h.b.Project(), "chg"))
	out, _ := json.Marshal([]any{ch, ms})
	return string(out)
}

func t2Changed() string {
	return strings.Replace(missionsFile("true"), `"title": "Second"`, `"title": "Second, revised"`, 1)
}

func t1Changed() string {
	return strings.Replace(missionsFile("true"), `"title": "First"`, `"title": "First, revised"`, 1)
}

func TestReloadKeepsUnchanged(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	before := h.mission("t1")
	ok, msg := h.reload(t2Changed(), "")
	if !ok || msg != "reloaded chg rev 2: 1 changed, 0 added, 0 removed, 1 unchanged" {
		t.Fatalf("reload: %v %q", ok, msg)
	}
	if t1 := h.mission("t1"); t1.State != InReview || t1.Attempt != before.Attempt || t1.Current.Task != before.Current.Task {
		t.Fatalf("t1 moved: %+v", t1)
	}
	if t2 := h.mission("t2"); t2.State != Pending || t2.Hash == "" || t2.Hash == before.Hash {
		t.Fatalf("t2: %+v", t2)
	}
	if ch, _, _ := h.c.Store.Change(h.ctx, "chg"); ch.Rev != 2 {
		t.Fatalf("rev %d", ch.Rev)
	}
}

func TestReloadRefusesInFlightWithoutForce(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.pass()
	before := h.snapshot()
	ok, msg := h.reload(t1Changed(), "")
	if ok || !strings.Contains(msg, "t1") || !strings.Contains(msg, "accepted") {
		t.Fatalf("reload: %v %q", ok, msg)
	}
	if h.snapshot() != before {
		t.Fatal("a refused reload wrote something")
	}
	ok, msg = h.reload(t1Changed(), "plan fix")
	if !ok {
		t.Fatalf("forced reload: %q", msg)
	}
	if t1 := h.mission("t1"); t1.State != Pending {
		t.Fatalf("t1 after forced reload: %s", t1.State)
	}
	h.pass()
	board, _ := h.b.Board(h.ctx)
	if board["chg-t1-1"].State != "superseded" {
		t.Fatalf("old task %q", board["chg-t1-1"].State)
	}
	thread := board["chg-t1-1"].Request.Thread
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Ref == thread && e.From == "conductor" && e.Type == bus.CmdReply }); n != 1 {
		t.Fatalf("%d replies on the old thread", n)
	}
}

func TestReloadNeverCancelsApproval(t *testing.T) {
	for _, s := range []State{Approved, Integrating} {
		h := newHarness(t, "true")
		h.toReview(map[string]string{"src/t1/a.go": "x"})
		h.setState("t1", s, nil)
		before := h.snapshot()
		if ok, msg := h.reload(t1Changed(), "even so"); ok || !strings.Contains(msg, string(s)) {
			t.Fatalf("%s: %v %q", s, ok, msg)
		}
		if h.snapshot() != before {
			t.Fatalf("%s: a refused reload wrote something", s)
		}
	}
}

func TestReloadForceKeepsIntegrated(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	h.pass() // t2 requested
	oldHash := h.mission("t1").Hash
	if ok, _ := h.reload(t1Changed(), ""); ok {
		t.Fatal("changed integrated mission reloaded without force")
	}
	ok, msg := h.reload(t1Changed(), "typo in the title")
	if !ok {
		t.Fatalf("forced: %q", msg)
	}
	t1 := h.mission("t1")
	if t1.State != Integrated || !strings.Contains(t1.IntegratedUnder, "rev 1") || !strings.Contains(t1.IntegratedUnder, oldHash) || t1.Hash == oldHash {
		t.Fatalf("t1: %+v", t1)
	}
	if t2 := h.mission("t2"); t2.State != Requested || t2.Attempt != 1 {
		t.Fatalf("t2 was reset: %+v", t2)
	}
}

func TestReloadAddsAndRemoves(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	f := `{"change": "chg", "reviewer": "codex", "test": "true", "missions": [
 {"id": "t1", "title": "First", "agent": "kimi", "allowed_paths": ["src/t1/**"], "test": "true", "brief": "plan.md#t1"},
 {"id": "t3", "title": "Third", "agent": "coder", "allowed_paths": ["src/t3/**"], "test": "true", "brief": "plan.md#t3"}]}`
	ok, msg := h.reload(f, "")
	if !ok || msg != "reloaded chg rev 2: 0 changed, 1 added, 1 removed, 1 unchanged" {
		t.Fatalf("reload: %v %q", ok, msg)
	}
	if t3 := h.mission("t3"); t3.State != Pending {
		t.Fatalf("t3: %+v", t3)
	}
	if t2 := h.mission("t2"); t2.State != Aborted || !strings.Contains(t2.Reason, "removed in rev 2") {
		t.Fatalf("t2: %+v", t2)
	}
}

func TestReloadRemovesInFlightWithForce(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	f := `{"change": "chg", "reviewer": "codex", "test": "true", "missions": [
 {"id": "t2", "title": "Second", "agent": "coder", "allowed_paths": ["src/t2/**"], "test": "true", "brief": "plan.md#t2"}]}`
	if ok, _ := h.reload(f, ""); ok {
		t.Fatal("removing an in-flight mission without force")
	}
	if ok, msg := h.reload(f, "dropped"); !ok {
		t.Fatalf("forced: %q", msg)
	}
	h.pass()
	if t1 := h.mission("t1"); t1.State != Aborted {
		t.Fatalf("t1: %s", t1.State)
	}
	if open, _ := h.b.OpenChallenges(h.ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate left open: %v", open)
	}
	board, _ := h.b.Board(h.ctx)
	if board["chg-t1-1"].State != "superseded" {
		t.Fatalf("old task %q", board["chg-t1-1"].State)
	}
	thread := board["chg-t1-1"].Request.Thread
	replies := func() int {
		return countCmds(h.cmds(), func(e bus.Event) bool { return e.Ref == thread && e.From == "conductor" && e.Type == bus.CmdReply })
	}
	n := replies()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.pass()
	if replies() != n+1 {
		t.Fatalf("late accept on a removed mission: %d → %d replies", n, replies())
	}
}

func TestReloadSupersededQueuedRequestProducesNothing(t *testing.T) {
	h := newHarness(t, "true")
	h.pass() // chg-t1-1 requested, not accepted
	if ok, msg := h.reload(t1Changed(), "rewrite"); !ok {
		t.Fatalf("reload: %q", msg)
	}
	h.pass() // supersede effects + attempt 2 published
	sha := h.commitAs(h.mission("t1").History[0].Base, "late-work", map[string]string{"src/t1/old.go": "x"})
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.b.CompleteRequestAt(h.ctx, "chg-t1-1", "kimi", "late", sha, "late-work")
	h.pass()
	h.pass()
	t1 := h.mission("t1")
	if t1.Current.Task != "chg-t1-2" || strings.Contains(t1.Subject, sha[:12]) || t1.State != Requested {
		t.Fatalf("the superseded delivery was used: %+v", t1)
	}
	var drained []string
	h.b.DrainCmd(h.ctx, "kimi", "test", 24*time.Hour, func(e bus.Event) error { drained = append(drained, e.Message); return nil })
	if !strings.Contains(strings.Join(drained, "\n"), "superseded by chg-t1-2") {
		t.Fatalf("drain lacks the superseded reply: %q", drained)
	}
}

func TestReloadAbortedChangeRefused(t *testing.T) {
	h := newHarness(t, "true")
	h.c.HandleOp(h.ctx, bus.MissionOp{Op: "abort", Args: []string{"chg", "stop"}})
	if ok, msg := h.reload(t2Changed(), ""); ok || !strings.Contains(msg, "aborted") {
		t.Fatalf("reload of an aborted change: %v %q", ok, msg)
	}
}

func TestReloadIsAtomic(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	before := h.snapshot()
	h.c.Store.Token = "lost" // the lease is gone when the batch runs
	if ok, _ := h.reload(t2Changed(), ""); ok {
		t.Fatal("a reload without the lease succeeded")
	}
	if h.snapshot() != before {
		t.Fatal("a failed reload left a partial revision")
	}
}

func TestPlanReloadTable(t *testing.T) {
	f, _ := Parse([]byte(missionsFile("true")))
	m1, _ := f.Mission("t1")
	rec := func(s State) map[string]MissionRecord {
		return map[string]MissionRecord{"t1": {ID: "t1", Hash: "old", State: s, Attempt: 1, Current: Attempt{N: 1, Task: "chg-t1-1", Agent: "kimi"}},
			"t2": {ID: "t2", Hash: func() string { m, _ := f.Mission("t2"); return m.Hash() }(), State: Pending}}
	}
	for _, c := range []struct {
		state      State
		force      bool
		wantErr    bool
		wantState  State
		superseded bool
	}{
		{Pending, false, false, Pending, true},
		{Ready, false, false, Pending, true},
		{Accepted, false, true, "", false},
		{Accepted, true, false, Pending, true},
		{Blocked, true, false, Pending, true},
		{InReview, true, false, Pending, true},
		{Approved, true, true, "", false},
		{Integrating, true, true, "", false},
		{Integrated, false, true, "", false},
		{Integrated, true, false, Integrated, false},
	} {
		got, counts, err := planReload(rec(c.state), f, 2, c.force, "why")
		if (err != nil) != c.wantErr {
			t.Errorf("%s force=%v: err %v", c.state, c.force, err)
			continue
		}
		if err != nil {
			continue
		}
		r := got["t1"]
		if r.State != c.wantState || r.Hash != m1.Hash() || (len(r.History) == 1) != c.superseded || counts.Changed != 1 || counts.Unchanged != 1 {
			t.Errorf("%s force=%v: %+v counts %+v", c.state, c.force, r, counts)
		}
		if _, touched := got["t2"]; touched {
			t.Errorf("%s: an unchanged mission was rewritten", c.state)
		}
	}
}

// CP1 (Codex): a mission removed by one revision and brought back by the next
// is active again, not stuck as a tombstone.
func TestReloadReintroducesTombstone(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	only1 := `{"change": "chg", "reviewer": "codex", "test": "true", "missions": [
 {"id": "t1", "title": "First", "agent": "kimi", "allowed_paths": ["src/t1/**"], "test": "true", "brief": "plan.md#t1"}]}`
	if ok, msg := h.reload(only1, ""); !ok {
		t.Fatalf("remove: %q", msg)
	}
	ok, msg := h.reload(missionsFile("true"), "")
	if !ok || msg != "reloaded chg rev 3: 0 changed, 1 added, 0 removed, 1 unchanged" {
		t.Fatalf("reintroduce: %v %q", ok, msg)
	}
	if t2 := h.mission("t2"); t2.State != Pending {
		t.Fatalf("t2 still %s", t2.State)
	}
}
