package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConductorLease(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if ok, err := b.AcquireConductor(ctx, "tok1", 15*time.Second); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if ok, _ := b.AcquireConductor(ctx, "tok2", 15*time.Second); ok {
		t.Fatal("second acquire succeeded")
	}
	if ok, _ := b.RenewConductor(ctx, "tok2", 15*time.Second); ok {
		t.Fatal("renew with the wrong token succeeded")
	}
	if ok, _ := b.RenewConductor(ctx, "tok1", 15*time.Second); !ok {
		t.Fatal("renew failed")
	}
	if alive, _ := b.ConductorAlive(ctx); !alive {
		t.Fatal("not alive")
	}
	if err := b.ReleaseConductor(ctx, "tok1"); err != nil {
		t.Fatal(err)
	}
	if alive, _ := b.ConductorAlive(ctx); alive {
		t.Fatal("alive after release")
	}
}

func TestConductorFencedWritesRefuseStaleToken(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "real", 15*time.Second)
	if _, err := b.Request(ctx, "chg-t1-1", "conductor", "kimi", "", "go", 0); err != nil {
		t.Fatal(err)
	}
	before := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Val()
	stale := "stale"
	checks := map[string]error{}
	checks["hash"] = b.FencedHashSet(ctx, stale, MissionsKey(b.project, "chg"), "t1", "{}")
	_, checks["request"] = b.FencedRequest(ctx, stale, "chg-t2-1", "conductor", "kimi", "", "go")
	checks["board"] = b.FencedBoardState(ctx, stale, "chg-t1-1", "superseded")
	checks["challenge"] = b.FencedChallenge(ctx, stale, "codex", "chg-t1-1", "conductor", "review")
	checks["resolve"] = b.FencedResolveChallenge(ctx, stale, "codex", "chg-t1-1")
	_, checks["cmd"] = b.FencedCmdRef(ctx, stale, "conductor", "kimi", CmdReply, "x", "stop", true)
	checks["result"] = b.MissionOpResult(ctx, stale, "1-0", true, "ok")
	for name, err := range checks {
		if !errors.Is(err, ErrNotHolder) {
			t.Errorf("%s: err = %v, want ErrNotHolder", name, err)
		}
	}
	if n := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Val(); n != before {
		t.Errorf("cmd stream grew under a stale token: %d → %d", before, n)
	}
	board, _ := b.Board(ctx)
	if _, ok := board["chg-t2-1"]; ok || board["chg-t1-1"].State != "requested" {
		t.Errorf("board changed under a stale token: %+v", board)
	}
	if n := b.r.Exists(ctx, MissionsKey(b.project, "chg"), GateKey(b.project, "codex"), MissionResultsKey(b.project)).Val(); n != 0 {
		t.Errorf("%d keys created under a stale token", n)
	}
}

func TestFencedRequestMatchesRequest(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	if _, err := b.Request(ctx, "plain", "conductor", "kimi", "", "go", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedRequest(ctx, "tok", "fenced", "conductor", "kimi", "", "go"); err != nil {
		t.Fatal(err)
	}
	board, _ := b.Board(ctx)
	p, f := board["plain"], board["fenced"]
	if p.State != f.State || p.Owner != f.Owner || f.Request == nil || p.Request.From != f.Request.From ||
		p.Request.Target != f.Request.Target || f.Request.Delivery != p.Request.Delivery || f.Request.ExpiresAt != 0 {
		t.Fatalf("shape differs:\nplain  %+v %+v\nfenced %+v %+v", p, p.Request, f, f.Request)
	}
	entries := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Val()
	if len(entries) != 2 {
		t.Fatalf("%d cmd entries", len(entries))
	}
	pv, fv := entries[0].Values, entries[1].Values
	for k := range pv {
		if k != "task" && pv[k] != fv[k] {
			t.Errorf("cmd field %s: plain %v, fenced %v", k, pv[k], fv[k])
		}
	}
	if len(pv) != len(fv) {
		t.Errorf("cmd fields differ: %v vs %v", pv, fv)
	}
	if _, err := b.FencedRequest(ctx, "tok", "fenced", "conductor", "kimi", "", "again"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
	if err := b.AcceptRequest(ctx, "fenced", "kimi"); err != nil {
		t.Fatalf("a fenced request must accept like any other: %v", err)
	}
}

func TestFencedChallengeOpensGateAndCmd(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	if err := b.FencedChallenge(ctx, "tok", "codex", "chg-t1-1", "conductor", "please review"); err != nil {
		t.Fatal(err)
	}
	open, _ := b.OpenChallenges(ctx, "codex")
	if open["chg-t1-1"] != "conductor|please review" {
		t.Fatalf("gate: %v", open)
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	last := evs[len(evs)-1]
	if last.Type != CmdChallenge || last.Target != "codex" || last.Ref != "chg-t1-1" || last.From != "conductor" {
		t.Fatalf("cmd: %+v", last)
	}
	for i := 0; i < 2; i++ { // idempotent: a missing ref is not an error
		if err := b.FencedResolveChallenge(ctx, "tok", "codex", "chg-t1-1"); err != nil {
			t.Fatal(err)
		}
	}
	if open, _ := b.OpenChallenges(ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate still open: %v", open)
	}
}

func TestFencedCmdRefNoWake(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	id, err := b.FencedCmdRef(ctx, "tok", "conductor", "kimi", CmdReply, "thread-1", "superseded", true)
	if err != nil {
		t.Fatal(err)
	}
	vals := b.r.XRange(ctx, StreamKey(b.project, "cmd"), id, id).Val()[0].Values
	if vals["wake"] != "no" || vals["ref"] != "thread-1" || vals["type"] != CmdReply {
		t.Fatalf("fields: %v", vals)
	}
	if _, err := b.FencedCmdRef(ctx, "tok", "conductor", "Bad Name", CmdReply, "", "x", false); err == nil {
		t.Fatal("invalid target accepted")
	}
}

func TestFencedBoardState(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	b.Request(ctx, "chg-t1-1", "conductor", "kimi", "", "go", 0)
	if err := b.FencedBoardState(ctx, "tok", "chg-t1-1", "superseded"); err != nil {
		t.Fatal(err)
	}
	board, _ := b.Board(ctx)
	if e := board["chg-t1-1"]; e.State != "superseded" || e.Request == nil || e.Request.Target != "kimi" {
		t.Fatalf("entry: %+v", e)
	}
	if err := b.FencedBoardState(ctx, "tok", "nope", "superseded"); err == nil {
		t.Fatal("unknown task accepted")
	}
}

func TestMissionOpsRoundTrip(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	id, err := b.SubmitMissionOp(ctx, "master", "retry", []string{"chg/t1"})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := b.ReadMissionOps(ctx, "conductor", "c1", 0)
	if err != nil || len(ops) != 1 || ops[0].ID != id || ops[0].Op != "retry" || ops[0].From != "master" || ops[0].Args[0] != "chg/t1" {
		t.Fatalf("read: %+v %v", ops, err)
	}
	if err := b.MissionOpResult(ctx, "tok", id, true, "retried"); err != nil {
		t.Fatal(err)
	}
	if err := b.AckMissionOp(ctx, "tok", "conductor", id); err != nil {
		t.Fatal(err)
	}
	ok, msg, found, err := b.WaitMissionOpResult(ctx, id, time.Second)
	if err != nil || !found || !ok || msg != "retried" {
		t.Fatalf("result: %v %q %v %v", ok, msg, found, err)
	}
	// results never come back as operations, and nothing stays pending
	if ops, _ := b.ReadMissionOps(ctx, "conductor", "c1", 0); len(ops) != 0 {
		t.Fatalf("result read back as an op: %+v", ops)
	}
	if p := b.r.XPending(ctx, MissionOpsKey(b.project), "conductor").Val(); p.Count != 0 {
		t.Fatalf("pending: %+v", p)
	}
	other, _ := b.SubmitMissionOp(ctx, "master", "abort", []string{"chg"})
	start := time.Now()
	if _, _, found, _ := b.WaitMissionOpResult(ctx, other, 300*time.Millisecond); found || time.Since(start) > 2*time.Second {
		t.Fatal("timeout must report found=false promptly")
	}
}

func TestFencedHashSetRefusesForeignKey(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	if err := b.FencedHashSet(ctx, "tok", AgentsKey(b.project), "x", "y"); err == nil {
		t.Fatal("foreign key accepted")
	}
	if err := b.FencedHashSet(ctx, "tok", ChangeKey(b.project, "chg"), "rec", "{}"); err != nil {
		t.Fatal(err)
	}
	if ms, err := b.ServerNowMS(ctx); err != nil || ms < time.Now().Add(-time.Hour).UnixMilli() {
		t.Fatalf("ServerNowMS = %d %v", ms, err)
	}
}

func TestAckMissionOpIsFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	id, _ := b.SubmitMissionOp(ctx, "master", "retry", []string{"chg/t1"})
	b.ReadMissionOps(ctx, "conductor", "c1", 0)
	if err := b.AckMissionOp(ctx, "stale", "conductor", id); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("stale ack: %v", err)
	}
	if p := b.r.XPending(ctx, MissionOpsKey(b.project), "conductor").Val(); p.Count != 1 {
		t.Fatalf("a stale token acknowledged the op: %+v", p)
	}
	if err := b.AckMissionOp(ctx, "tok", "conductor", id); err != nil {
		t.Fatal(err)
	}
}

// An op read by a conductor that crashed before acknowledging it is handed to
// the next reader, whatever its consumer name.
func TestReadMissionOpsRecoversPending(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	id, _ := b.SubmitMissionOp(ctx, "master", "retry", []string{"chg/t1"})
	if ops, _ := b.ReadMissionOps(ctx, "conductor", "crashed", 0); len(ops) != 1 {
		t.Fatal("first read")
	}
	ops, err := b.ReadMissionOps(ctx, "conductor", "restarted", 0)
	if err != nil || len(ops) != 1 || ops[0].ID != id || ops[0].Op != "retry" {
		t.Fatalf("pending op not recovered: %+v %v", ops, err)
	}
}

func TestFencedMissionBatch(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", 15*time.Second)
	if err := b.FencedMissionBatch(ctx, "stale", "chg", map[string]string{"t1": "{}"}, `{"rev":2}`); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("stale: %v", err)
	}
	if n := b.r.Exists(ctx, MissionsKey(b.project, "chg"), ChangeKey(b.project, "chg")).Val(); n != 0 {
		t.Fatal("a stale batch wrote something")
	}
	if err := b.FencedMissionBatch(ctx, "tok", "chg", map[string]string{"t1": `{"id":"t1"}`, "t2": `{"id":"t2"}`}, `{"rev":2}`); err != nil {
		t.Fatal(err)
	}
	ms, _ := b.ReadMissionHash(ctx, MissionsKey(b.project, "chg"))
	ch, _ := b.ReadMissionHash(ctx, ChangeKey(b.project, "chg"))
	if len(ms) != 2 || ch[ChangeRecordField] != `{"rev":2}` {
		t.Fatalf("missions %v change %v", ms, ch)
	}
	if err := b.FencedMissionBatch(ctx, "tok", "Bad Name", nil, "{}"); err == nil {
		t.Fatal("invalid change name accepted")
	}
}

func TestFencedCmdWithChangeAtomic(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, ConductorKey(b.project), ChangeKey(b.project, "chg")) })
	b.AcquireConductor(ctx, "real", 15*time.Second)
	if _, err := b.FencedCmdWithChange(ctx, "stale", "chg", "conductor", "master", CmdComplete, "done", `{"rev":1}`); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("stale token: %v, want ErrNotHolder", err)
	}
	if n, _ := b.r.Exists(ctx, ChangeKey(b.project, "chg")).Result(); n != 0 {
		t.Fatal("a refused write left the change record")
	}
	id, err := b.FencedCmdWithChange(ctx, "real", "chg", "conductor", "master", CmdComplete, "change chg rev 1 complete", `{"rev":1}`)
	if err != nil || id == "" {
		t.Fatalf("write: %q %v", id, err)
	}
	rec, _ := b.r.HGet(ctx, ChangeKey(b.project, "chg"), ChangeRecordField).Result()
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 10)
	if rec != `{"rev":1}` || len(evs) != 1 || evs[0].Type != CmdComplete || evs[0].Target != "master" {
		t.Fatalf("record %q, cmds %+v", rec, evs)
	}
}

func TestResultBlockNeverRoundsToForever(t *testing.T) {
	// XREAD BLOCK takes milliseconds and BLOCK 0 waits forever: what is left
	// under a millisecond must end the wait, not become 0.
	for _, c := range []struct {
		left time.Duration
		want time.Duration
		ok   bool
	}{
		{0, 0, false}, {500 * time.Microsecond, 0, false}, {time.Millisecond, time.Millisecond, true},
		{300 * time.Millisecond, 300 * time.Millisecond, true}, {5 * time.Second, time.Second, true},
	} {
		if got, ok := resultBlock(c.left); got != c.want || ok != c.ok {
			t.Errorf("resultBlock(%v) = %v, %v; want %v, %v", c.left, got, ok, c.want, c.ok)
		}
	}
}
