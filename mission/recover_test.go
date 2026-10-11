package mission

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// loseRedis deletes the change and its mission records, as a lost store would.
func (h *harness) loseRedis() {
	h.r.Del(h.ctx, bus.ChangeKey(h.b.Project(), "chg"), bus.MissionsKey(h.b.Project(), "chg"))
}

func (h *harness) recoverOp(data string) (bool, string) {
	h.t.Helper()
	commit := commitFile(h.t, h.repo, "docs/changes/chg/missions.json", data, "recover from")
	return h.c.HandleOp(h.ctx, bus.MissionOp{Op: "load", From: "master", Args: []string{"docs/changes/chg/missions.json@" + commit, "recover"}})
}

func (h *harness) integrateT1() {
	h.t.Helper()
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	if m := h.mission("t1"); m.State != Integrated {
		h.t.Fatalf("t1 not integrated: %s", m.State)
	}
}

func TestRecoverRebuilds(t *testing.T) {
	h := newHarness(t, "true")
	h.integrateT1()
	commit := h.mission("t1").Current.Commit
	h.loseRedis()
	ok, msg := h.recoverOp(missionsFile("true") + "\n")
	if !ok || msg != "recovered chg rev 1: 1 integrated, 0 blocked, 1 pending" {
		t.Fatalf("recover: %v %q", ok, msg)
	}
	if t1 := h.mission("t1"); t1.State != Integrated || t1.Current.Commit != commit || !t1.TestedOK {
		t.Fatalf("t1: %+v", t1)
	}
	h.pass()
	if t2 := h.mission("t2"); t2.State != Requested {
		t.Fatalf("t2 after recover: %s", t2.State)
	}
}

func TestRecoverChangedDefinitionBlocks(t *testing.T) {
	h := newHarness(t, "true")
	h.integrateT1()
	h.loseRedis()
	ok, msg := h.recoverOp(t1Changed())
	if !ok || !strings.Contains(msg, "1 blocked") {
		t.Fatalf("recover: %v %q", ok, msg)
	}
	if t1 := h.mission("t1"); t1.State != Blocked || !strings.Contains(t1.Reason, "merged under another definition") {
		t.Fatalf("t1: %+v", t1)
	}
	h.pass()
	h.pass()
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func TestRecoverRefusedWhenKnown(t *testing.T) {
	h := newHarness(t, "true")
	if ok, msg := h.recoverOp(missionsFile("true") + "\n"); ok || !strings.Contains(msg, "already loaded") {
		t.Fatalf("recover of a known change: %v %q", ok, msg)
	}
}

func TestRecoverMostRecentMergeDecides(t *testing.T) {
	f, _ := Parse([]byte(missionsFile("true")))
	m1, _ := f.Mission("t1")
	cur, old := m1.Hash(), "oldhash"
	merges := func(hashes ...string) []MissionMerge {
		var out []MissionMerge
		for i, hs := range hashes {
			out = append(out, MissionMerge{ID: "t1", Commit: strings.Repeat("a", 40), Hash: hs, Rev: i + 1})
		}
		return out
	}
	if r, _ := recoverRecords(f, merges(old, cur)); r["t1"].State != Integrated {
		t.Fatalf("old then current: %s", r["t1"].State)
	}
	if r, _ := recoverRecords(f, merges(cur, old)); r["t1"].State != Blocked {
		t.Fatalf("current then old: %s", r["t1"].State)
	}
	if _, rev := recoverRecords(f, merges(old, cur)); rev != 2 {
		t.Fatalf("rev %d", rev)
	}
}

func TestNewAttemptSkipsTakenNames(t *testing.T) {
	h := newHarness(t, "true")
	h.integrateT1()
	h.pass() // chg-t2-1 requested
	h.loseRedis()
	if ok, msg := h.recoverOp(missionsFile("true") + "\n"); !ok {
		t.Fatalf("recover: %q", msg)
	}
	h.pass()
	if t2 := h.mission("t2"); t2.Current.Task != "chg-t2-2" {
		t.Fatalf("t2 reused a taken name: %+v", t2.Current)
	}
}
