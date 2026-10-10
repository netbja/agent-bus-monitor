package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// dialDispatch is dialTest plus a full DeleteProject cleanup, because the
// dispatch lease and wake hashes are not in dialTest's fixed key list.
func dialDispatch(t *testing.T) *Bus {
	t.Helper()
	b := dialTest(t)
	t.Cleanup(func() { _, _ = DeleteProject(context.Background(), b.r, b.project) })
	return b
}

func TestDispatchKeyNaming(t *testing.T) {
	if got := DispatchKey("dev"); got != "dev:dispatch" {
		t.Fatalf("DispatchKey = %q, want dev:dispatch", got)
	}
	if got := WakeKey("dev", "coder"); got != "dev:wake:coder" {
		t.Fatalf("WakeKey = %q, want dev:wake:coder", got)
	}
}

func TestDispatchLeaseExclusive(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()

	if alive, err := b.DispatchAlive(ctx); err != nil || alive {
		t.Fatalf("DispatchAlive before acquire = %v, %v; want false", alive, err)
	}
	ok, err := b.AcquireDispatch(ctx, "tok-a", 15*time.Second)
	if err != nil || !ok {
		t.Fatalf("AcquireDispatch(a) = %v, %v; want true", ok, err)
	}
	ok, err = b.AcquireDispatch(ctx, "tok-b", 15*time.Second)
	if err != nil || ok {
		t.Fatalf("AcquireDispatch(b) while a holds = %v, %v; want false", ok, err)
	}
	if h, _ := b.HoldsDispatch(ctx, "tok-a"); !h {
		t.Error("HoldsDispatch(a) = false, want true")
	}
	if h, _ := b.HoldsDispatch(ctx, "tok-b"); h {
		t.Error("HoldsDispatch(b) = true, want false")
	}
	if alive, err := b.DispatchAlive(ctx); err != nil || !alive {
		t.Fatalf("DispatchAlive while held = %v, %v; want true", alive, err)
	}
	// A non-holder's release is a no-op.
	if err := b.ReleaseDispatch(ctx, "tok-b"); err != nil {
		t.Fatalf("ReleaseDispatch(b): %v", err)
	}
	if h, _ := b.HoldsDispatch(ctx, "tok-a"); !h {
		t.Fatal("a non-holder's release removed the lease")
	}
	if err := b.ReleaseDispatch(ctx, "tok-a"); err != nil {
		t.Fatalf("ReleaseDispatch(a): %v", err)
	}
	if alive, _ := b.DispatchAlive(ctx); alive {
		t.Fatal("lease survived its holder's release")
	}
	if ok, err := b.AcquireDispatch(ctx, "tok-b", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch(b) after release = %v, %v; want true", ok, err)
	}
}

func TestRenewDetectsLoss(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()

	if ok, _ := b.AcquireDispatch(ctx, "tok-a", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch(a) failed")
	}
	if ok, err := b.RenewDispatch(ctx, "tok-a", 15*time.Second); err != nil || !ok {
		t.Fatalf("RenewDispatch while held = %v, %v; want true", ok, err)
	}
	// Lose the lease (expiry is simulated by deleting it) and let b take it.
	b.r.Del(ctx, DispatchKey(b.project))
	if ok, err := b.RenewDispatch(ctx, "tok-a", 15*time.Second); err != nil || ok {
		t.Fatalf("RenewDispatch after loss = %v, %v; want false", ok, err)
	}
	if alive, _ := b.DispatchAlive(ctx); alive {
		t.Fatal("a failed renew recreated the lease")
	}
	if ok, _ := b.AcquireDispatch(ctx, "tok-b", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch(b) failed")
	}
	if ok, _ := b.RenewDispatch(ctx, "tok-a", 15*time.Second); ok {
		t.Fatal("old holder renewed the new holder's lease")
	}
	if h, _ := b.HoldsDispatch(ctx, "tok-b"); !h {
		t.Fatal("old holder's renew disturbed the new holder")
	}
	if ok, _ := b.RenewDispatch(ctx, "tok-b", 15*time.Second); !ok {
		t.Fatal("RenewDispatch(b) = false, want true")
	}
}

func TestReserveWakeWindow(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	const retry = 50 * time.Millisecond

	next := WakeRecord{State: "prompting", Tries: 1, HighID: "1-0", PID: 42, PIDStart: 9000,
		LastDelivered: "0-0", PendingTotal: 3, Reason: "obligation"}
	rec, ok, err := b.ReserveWake(ctx, "coder", "tok", next, retry, time.Second)
	if err != nil || !ok {
		t.Fatalf("first ReserveWake = %v, %v; want reserved", ok, err)
	}
	if rec.At == 0 {
		t.Fatal("reservation did not stamp At from server time")
	}
	want := next
	want.At = rec.At
	if rec != want {
		t.Fatalf("stored record = %+v, want %+v", rec, want)
	}
	if got, exists, err := b.Wake(ctx, "coder"); err != nil || !exists || got != rec {
		t.Fatalf("Wake = %+v, %v, %v; want %+v, true", got, exists, err, rec)
	}

	// Inside the window: refused, and the current record comes back unchanged.
	again := next
	again.Tries = 2
	cur, ok, err := b.ReserveWake(ctx, "coder", "tok", again, retry, time.Second)
	if err != nil || ok {
		t.Fatalf("ReserveWake inside window = %v, %v; want false, nil", ok, err)
	}
	if cur != rec {
		t.Fatalf("refused reservation returned %+v, want the stored %+v", cur, rec)
	}

	time.Sleep(retry + 20*time.Millisecond)
	cur, ok, err = b.ReserveWake(ctx, "coder", "tok", again, retry, time.Second)
	if err != nil || !ok {
		t.Fatalf("ReserveWake after window = %v, %v; want reserved", ok, err)
	}
	if cur.Tries != 2 || cur.At < rec.At+retry.Milliseconds() {
		t.Fatalf("second reservation = %+v; want tries 2 and At ≥ %d", cur, rec.At+retry.Milliseconds())
	}
}

func TestReserveWakeRequiresLeaseAndPTTL(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	next := WakeRecord{State: "prompting", Tries: 1}

	if _, ok, err := b.ReserveWake(ctx, "coder", "tok", next, time.Minute, time.Second); !errors.Is(err, ErrNotHolder) || ok {
		t.Fatalf("ReserveWake without lease = %v, %v; want ErrNotHolder", ok, err)
	}
	if ok, _ := b.AcquireDispatch(ctx, "other", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	if _, ok, err := b.ReserveWake(ctx, "coder", "tok", next, time.Minute, time.Second); !errors.Is(err, ErrNotHolder) || ok {
		t.Fatalf("ReserveWake with someone else's lease = %v, %v; want ErrNotHolder", ok, err)
	}
	b.r.Del(ctx, DispatchKey(b.project))

	// Held, but with less PTTL left than required: not reserved, not an error.
	if ok, _ := b.AcquireDispatch(ctx, "tok", 2*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	if _, ok, err := b.ReserveWake(ctx, "coder", "tok", next, time.Minute, 5*time.Second); err != nil || ok {
		t.Fatalf("ReserveWake with low PTTL = %v, %v; want false, nil", ok, err)
	}
	if _, exists, _ := b.Wake(ctx, "coder"); exists {
		t.Fatal("a refused reservation wrote the wake record")
	}
	if _, _, err := b.ReserveWake(ctx, "Bad Agent", "tok", next, time.Minute, time.Second); err == nil {
		t.Fatal("ReserveWake accepted an invalid agent name")
	}
}

func TestFencedWritesHoldingLease(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}

	rec := WakeRecord{State: "deferred", At: 123, Tries: 2, HighID: "5-1", PID: 7, PIDStart: 1 << 40,
		Escalated: "pending", LastDelivered: "4-0", PendingTotal: 9, GapNotified: true, Reason: "focused"}
	if err := b.PutWake(ctx, "coder", "tok", rec); err != nil {
		t.Fatalf("PutWake: %v", err)
	}
	got, exists, err := b.Wake(ctx, "coder")
	if err != nil || !exists || got != rec {
		t.Fatalf("Wake = %+v, %v, %v; want %+v", got, exists, err, rec)
	}
	// PutWake replaces: cleared fields do not linger.
	if err := b.PutWake(ctx, "coder", "tok", WakeRecord{State: "prompted"}); err != nil {
		t.Fatalf("PutWake: %v", err)
	}
	if got, _, _ := b.Wake(ctx, "coder"); got != (WakeRecord{State: "prompted"}) {
		t.Fatalf("PutWake did not replace: %+v", got)
	}
	if err := b.DeleteWake(ctx, "coder", "tok"); err != nil {
		t.Fatalf("DeleteWake: %v", err)
	}
	if _, exists, _ := b.Wake(ctx, "coder"); exists {
		t.Fatal("DeleteWake left the record")
	}

	id, err := b.FencedCmd(ctx, "tok", "dispatch", "master", CmdEscalation, "coder stuck")
	if err != nil {
		t.Fatalf("FencedCmd: %v", err)
	}
	msgs, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), id, id).Result()
	if len(msgs) != 1 {
		t.Fatalf("FencedCmd entry %s not found", id)
	}
	wantCmd := map[string]interface{}{"from": "dispatch", "target": "master", "type": CmdEscalation,
		"ref": "", "command": "coder stuck", "text_complete": "yes"}
	if len(msgs[0].Values) != len(wantCmd) {
		t.Fatalf("FencedCmd fields = %v, want %v", msgs[0].Values, wantCmd)
	}
	for k, v := range wantCmd {
		if msgs[0].Values[k] != v {
			t.Errorf("FencedCmd field %s = %v, want %v", k, msgs[0].Values[k], v)
		}
	}

	id, err = b.FencedNotify(ctx, "tok", "dispatch", "coder escalated")
	if err != nil {
		t.Fatalf("FencedNotify: %v", err)
	}
	msgs, _ = b.r.XRange(ctx, StreamKey(b.project, "notify"), id, id).Result()
	if len(msgs) != 1 || len(msgs[0].Values) != 2 || msgs[0].Values["from"] != "dispatch" || msgs[0].Values["message"] != "coder escalated" {
		t.Fatalf("FencedNotify entry = %v", msgs)
	}

	// Validation happens before the script, like Bus.Cmd.
	if _, err := b.FencedCmd(ctx, "tok", "dispatch", "Bad Target", CmdEscalation, "x"); err == nil {
		t.Error("FencedCmd accepted an invalid target")
	}
	if _, err := b.FencedCmd(ctx, "tok", "dispatch", "master", "bogus", "x"); err == nil {
		t.Error("FencedCmd accepted an invalid type")
	}
	if _, err := b.FencedCmd(ctx, "tok", "dispatch", "master", CmdEscalation, "\xff"); err == nil {
		t.Error("FencedCmd accepted invalid UTF-8")
	}
	if err := b.PutWake(ctx, "Bad Agent", "tok", rec); err == nil {
		t.Error("PutWake accepted an invalid agent")
	}
	if err := b.DeleteWake(ctx, "Bad Agent", "tok"); err == nil {
		t.Error("DeleteWake accepted an invalid agent")
	}
	if _, _, err := b.Wake(ctx, "Bad Agent"); err == nil {
		t.Error("Wake accepted an invalid agent")
	}
}

func TestFencedWritesRefuseWithoutLease(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	rec := WakeRecord{State: "prompting", Tries: 1}

	check := func(label string) {
		t.Helper()
		if err := b.PutWake(ctx, "coder", "tok", rec); !errors.Is(err, ErrNotHolder) {
			t.Errorf("%s: PutWake err = %v, want ErrNotHolder", label, err)
		}
		if err := b.DeleteWake(ctx, "coder", "tok"); !errors.Is(err, ErrNotHolder) {
			t.Errorf("%s: DeleteWake err = %v, want ErrNotHolder", label, err)
		}
		if _, err := b.FencedCmd(ctx, "tok", "dispatch", "master", CmdEscalation, "x"); !errors.Is(err, ErrNotHolder) {
			t.Errorf("%s: FencedCmd err = %v, want ErrNotHolder", label, err)
		}
		if _, err := b.FencedNotify(ctx, "tok", "dispatch", "x"); !errors.Is(err, ErrNotHolder) {
			t.Errorf("%s: FencedNotify err = %v, want ErrNotHolder", label, err)
		}
		for _, k := range []string{WakeKey(b.project, "coder"), StreamKey(b.project, "cmd"), StreamKey(b.project, "notify")} {
			if n, _ := b.r.Exists(ctx, k).Result(); n != 0 {
				t.Errorf("%s: refused write created %s", label, k)
			}
		}
	}
	check("no lease")
	if ok, _ := b.AcquireDispatch(ctx, "other", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	check("foreign lease")
}

func TestFencedWritesDoNotRecreateDeletedProject(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	rec := WakeRecord{State: "prompting", Tries: 1}
	if err := b.PutWake(ctx, "coder", "tok", rec); err != nil {
		t.Fatalf("PutWake: %v", err)
	}
	if _, err := b.FencedCmd(ctx, "tok", "dispatch", "master", CmdEscalation, "x"); err != nil {
		t.Fatalf("FencedCmd: %v", err)
	}
	if _, err := b.FencedNotify(ctx, "tok", "dispatch", "x"); err != nil {
		t.Fatalf("FencedNotify: %v", err)
	}
	before, _ := ProjectKeys(ctx, b.r, b.project)
	if _, err := DeleteProject(ctx, b.r, b.project); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if err := b.PutWake(ctx, "coder", "tok", rec); !errors.Is(err, ErrNotHolder) {
		t.Errorf("PutWake err = %v, want ErrNotHolder", err)
	}
	if _, _, err := b.ReserveWake(ctx, "coder", "tok", rec, time.Millisecond, time.Millisecond); !errors.Is(err, ErrNotHolder) {
		t.Errorf("ReserveWake err = %v, want ErrNotHolder", err)
	}
	if err := b.DeleteWake(ctx, "coder", "tok"); !errors.Is(err, ErrNotHolder) {
		t.Errorf("DeleteWake err = %v, want ErrNotHolder", err)
	}
	if _, err := b.FencedCmd(ctx, "tok", "dispatch", "master", CmdEscalation, "x"); !errors.Is(err, ErrNotHolder) {
		t.Errorf("FencedCmd err = %v, want ErrNotHolder", err)
	}
	if _, err := b.FencedNotify(ctx, "tok", "dispatch", "x"); !errors.Is(err, ErrNotHolder) {
		t.Errorf("FencedNotify err = %v, want ErrNotHolder", err)
	}
	if ok, _ := b.RenewDispatch(ctx, "tok", 15*time.Second); ok {
		t.Error("RenewDispatch recreated the deleted lease")
	}

	keys := append(before, DispatchKey(b.project), WakeKey(b.project, "coder"),
		StreamKey(b.project, "cmd"), StreamKey(b.project, "notify"))
	for _, k := range keys {
		if n, _ := b.r.Exists(ctx, k).Result(); n != 0 {
			t.Errorf("fenced write resurrected %s", k)
		}
	}
	if after, _ := ProjectKeys(ctx, b.r, b.project); len(after) != 0 {
		t.Errorf("deleted project has keys again: %v", after)
	}
}

func TestProjectsSeesDispatchKey(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if p, ok := projectFromKey(DispatchKey("dev")); !ok || p != "dev" {
		t.Fatalf("projectFromKey(dev:dispatch) = %q, %v; want dev, true", p, ok)
	}
	// A project whose only key is the dispatch lease is still discoverable.
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	list, err := Projects(ctx, b.r)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	for _, p := range list {
		if p.Project == b.project {
			return
		}
	}
	t.Fatalf("Projects did not find %q from its dispatch key alone", b.project)
}

func TestWakeKeyDoesNotForgeProject(t *testing.T) {
	// An agent named after a project kind is the most likely forger.
	for _, agent := range []string{"coder", "dispatch", "status", "wake"} {
		if p, ok := projectFromKey(WakeKey("dev", agent)); ok {
			t.Errorf("projectFromKey(%q) = %q, true; want not-a-project", WakeKey("dev", agent), p)
		}
	}
}
