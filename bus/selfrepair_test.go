package bus

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// Self-repair's intent (self-repair spec 4.1, step 2): the role's swap
// intent with kind self-repair and a nonce, taken and ended only by the
// dispatch lease holder, ended only by its own nonce, so one relauncher at a
// time and none next to a reload, a swap or a retire.
func TestSelfRepairIntentRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	ev := SwapEvent{By: "dispatch", Reason: "process gone 70s", OldPID: 241, OldPane: "w1:p2"}

	if _, err := b.Status(ctx, "foureyes", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n1", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("no lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.BeginSelfRepair(ctx, "other", "foureyes", "n1", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("another token: %v", err)
	}
	if live, _ := b.Swapping(ctx, "foureyes"); live {
		t.Fatal("a refused self-repair left an intent")
	}
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "", ev); err == nil {
		t.Fatal("an intent without a nonce")
	}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "n1", ev); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("a role with no snapshot: %v", err)
	}

	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n1", ev); err != nil {
		t.Fatalf("BeginSelfRepair: %v", err)
	}
	in, held, err := b.SwapIntentOf(ctx, "foureyes")
	if err != nil || !held || in.Kind != SwapKindSelfRepair || in.Nonce != "n1" || in.By != "dispatch" || in.OldPID != 241 {
		t.Fatalf("intent %+v held %v err %v", in, held, err)
	}
	// foureyes (step 2): the intent expires as a swap's does, SwapTTL from its begin.
	if ttl := b.r.PTTL(ctx, SwapKey(b.project, "foureyes")).Val(); ttl <= SwapTTL-5*time.Second || ttl > SwapTTL {
		t.Fatalf("self-repair intent TTL %v, want about %v", ttl, SwapTTL)
	}
	log, _ := b.SwapLog(ctx, "foureyes", 0)
	if len(log) != 1 || log[0].Event != SwapBegin || log[0].Kind != SwapKindSelfRepair || log[0].Reason != "process gone 70s" {
		t.Fatalf("ledger %+v", log)
	}
	// One relauncher at a time: a second self-repair, a reload and a swap are refused.
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n2", ev); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("second self-repair: %v", err)
	}
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{Kind: SwapKindReload, By: "sentinel"}); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("a reload next to it: %v", err)
	}

	// Right before the split: still its own nonce and still the holder.
	for _, c := range []struct {
		token, nonce string
		want         bool
	}{{"tok", "n1", true}, {"tok", "n2", false}, {"other", "n1", false}} {
		if got, err := b.SelfRepairHeld(ctx, c.token, "foureyes", c.nonce); err != nil || got != c.want {
			t.Errorf("held(%s, %s) = %v, %v; want %v", c.token, c.nonce, got, err, c.want)
		}
	}

	// The end: only the holder, only its own nonce.
	if err := b.EndSelfRepair(ctx, "other", "foureyes", "n1"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("end by another token: %v", err)
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n2"); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("end by another nonce: %v", err)
	}
	if live, _ := b.Swapping(ctx, "foureyes"); !live {
		t.Fatal("a refused end removed the intent")
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); err != nil {
		t.Fatalf("EndSelfRepair: %v", err)
	}
	if _, held, _ := b.SwapIntentOf(ctx, "foureyes"); held {
		t.Fatal("the intent outlived its end")
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); err != nil {
		t.Fatalf("an end already done: %v", err)
	}
	if got, _ := b.SelfRepairHeld(ctx, "tok", "foureyes", "n1"); got {
		t.Fatal("held after its end")
	}

	// Another kind's intent is never ended by a self-repair, even with its nonce.
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{Kind: SwapKindRetire, Nonce: "n1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("a retire's intent ended: %v", err)
	}
	if ttl := b.r.PTTL(ctx, SwapKey(b.project, "foureyes")).Val(); ttl <= 0 || ttl > SwapTTL {
		t.Fatalf("intent TTL %v", ttl)
	}
}

func TestSwapKindWord(t *testing.T) {
	for kind, want := range map[string]string{"": "swap", SwapKindReload: "reload", SwapKindRetire: "retire", SwapKindSelfRepair: "self-repair", "odd": "swap"} {
		if got := SwapKindWord(kind); got != want {
			t.Errorf("%q: %q, want %q", kind, got, want)
		}
	}
}

// The shadow's record (step 6): a would-relaunch ledger entry, kind
// self-repair, written only by the dispatch lease holder; WouldRelaunchLogged
// finds an episode's entry so a death is recorded once.
func TestFencedWouldRelaunchRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	ev := SwapEvent{Role: "coder", By: "dispatch", Reason: "dead 2m", OldPID: 241, OldPane: "w1:p2", Episode: "241:99"}
	if err := b.LogWouldRelaunch(ctx, "tok", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("no lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := b.LogWouldRelaunch(ctx, "other", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("another token: %v", err)
	}
	if logged, err := b.WouldRelaunchLogged(ctx, "coder", "241:99"); err != nil || logged {
		t.Fatalf("before: %v %v", logged, err)
	}
	if err := b.LogWouldRelaunch(ctx, "tok", ev); err != nil {
		t.Fatal(err)
	}
	got, err := b.SwapLog(ctx, "", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ledger %+v %v", got, err)
	}
	if e := got[0]; e.Event != SwapWouldRelaunch || e.Kind != SwapKindSelfRepair || e.Role != "coder" || e.Episode != "241:99" || e.OldPID != 241 || e.Reason != "dead 2m" {
		t.Fatalf("entry %+v", e)
	}
	if logged, _ := b.WouldRelaunchLogged(ctx, "coder", "241:99"); !logged {
		t.Fatal("the episode's entry not found")
	}
	for _, other := range [][2]string{{"coder", "242:1"}, {"foureyes", "241:99"}} {
		if logged, _ := b.WouldRelaunchLogged(ctx, other[0], other[1]); logged {
			t.Fatalf("%v read as logged", other)
		}
	}
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "Bad Role", Episode: "1:1"}); err == nil {
		t.Fatal("an invalid role accepted")
	}
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "coder"}); err == nil {
		t.Fatal("no episode accepted")
	}
}

// A would-relaunch is a record, not an end: written after a begin whose
// caller died, it does not hide that begin from Interrupted, which
// closeInterrupted closes (foureyes, self-repair 6a).
func TestWouldRelaunchHidesNoInterruptedBeginRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSwap(ctx, "coder"); err != nil { // the caller died: no terminal entry
		t.Fatal(err)
	}
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "coder", By: "dispatch", Episode: "241:99"}); err != nil {
		t.Fatal(err)
	}
	if bad, ev, err := b.Interrupted(ctx, "coder"); err != nil || !bad || ev.Event != SwapBegin {
		t.Fatalf("interrupted = %v %+v %v", bad, ev, err)
	}
	// Only would-relaunch entries are looked past: a terminal entry still ends it.
	if err := b.LogSwap(ctx, SwapEvent{Role: "coder", Event: SwapFailed, By: "master"}); err != nil {
		t.Fatal(err)
	}
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "coder", By: "dispatch", Episode: "242:1"}); err != nil {
		t.Fatal(err)
	}
	if bad, ev, err := b.Interrupted(ctx, "coder"); err != nil || bad {
		t.Fatalf("a closed begin reads interrupted: %+v %v", ev, err)
	}
	// A role whose ledger holds only would-relaunch entries has nothing interrupted.
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "foureyes", By: "dispatch", Episode: "7:7"}); err != nil {
		t.Fatal(err)
	}
	if bad, _, err := b.Interrupted(ctx, "foureyes"); err != nil || bad {
		t.Fatalf("foureyes: %v %v", bad, err)
	}
}

// A self-repair's end (step 7): its relaunched or failed entry and the end of
// its intent in one fenced script, only by the lease holder and only while
// the intent is its own (or already expired).
func TestFinishSelfRepairRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "coder", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	begin := SwapEvent{By: "dispatch", OldPID: 241, OldPane: "w1:p2", Episode: "241:99"}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "n1", begin); err != nil {
		t.Fatal(err)
	}
	done := SwapEvent{Role: "coder", Event: SwapRelaunched, By: "dispatch", OldPID: 241, NewPID: 902, NewPane: "w1:p5", Episode: "241:99"}
	if err := b.FinishSelfRepair(ctx, "other", "n1", done); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("another token: %v", err)
	}
	if err := b.FinishSelfRepair(ctx, "tok", "n2", done); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("another nonce: %v", err)
	}
	if err := b.FinishSelfRepair(ctx, "tok", "n1", SwapEvent{Role: "coder", Event: SwapDone, Episode: "241:99"}); err == nil {
		t.Fatal("a done entry ends a self-repair")
	}
	if log, _ := b.SwapLog(ctx, "coder", 0); len(log) != 1 {
		t.Fatalf("a refused finish wrote: %+v", log)
	}
	if live, _ := b.Swapping(ctx, "coder"); !live {
		t.Fatal("a refused finish ended the intent")
	}
	if err := b.FinishSelfRepair(ctx, "tok", "n1", done); err != nil {
		t.Fatalf("FinishSelfRepair: %v", err)
	}
	if live, _ := b.Swapping(ctx, "coder"); live {
		t.Fatal("the intent outlived its finish")
	}
	log, _ := b.SwapLog(ctx, "coder", 0)
	if len(log) != 2 || log[0].Event != SwapRelaunched || log[0].Kind != SwapKindSelfRepair || log[0].NewPID != 902 || log[0].Episode != "241:99" {
		t.Fatalf("ledger %+v", log)
	}
	if bad, _, err := b.Interrupted(ctx, "coder"); err != nil || bad {
		t.Fatalf("a finished self-repair reads interrupted: %v %v", bad, err)
	}
	// An intent already expired: the entry is still written (the act happened).
	fail := SwapEvent{Role: "coder", Event: SwapFailed, By: "dispatch", Episode: "902:5", Error: "no proof in 60s"}
	if err := b.FinishSelfRepair(ctx, "tok", "n3", fail); err != nil {
		t.Fatalf("finish after expiry: %v", err)
	}
	if log, _ := b.SwapLog(ctx, "coder", 1); len(log) != 1 || log[0].Event != SwapFailed || log[0].Kind != SwapKindSelfRepair || log[0].Error != "no proof in 60s" {
		t.Fatalf("failed entry %+v", log)
	}
}

// What the budget reads (4.5, step 7): each role's self-repair attempts (its
// begins), the death episodes whose relaunch failed its proof, and the crash
// loop the dispatcher marked, all kept in {p}:selfrepair by the fenced
// scripts that begin and end a self-repair, and cleared by a reset.
func TestSelfRepairPastRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	for _, r := range []string{"coder", "foureyes"} {
		if _, err := b.Status(ctx, r, "idle", "", AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.MarkCrashLoop(ctx, "tok", "coder"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("crash loop without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	attempt := func(role, nonce, ep, event string) {
		t.Helper()
		if err := b.BeginSelfRepair(ctx, "tok", role, nonce, SwapEvent{By: "dispatch", Episode: ep}); err != nil {
			t.Fatal(err)
		}
		if err := b.FinishSelfRepair(ctx, "tok", nonce, SwapEvent{Role: role, Event: event, By: "dispatch", Episode: ep}); err != nil {
			t.Fatal(err)
		}
	}
	attempt("coder", "a", "1:1", SwapRelaunched)
	attempt("coder", "b", "2:2", SwapFailed)
	// Neither a reload's begin nor a shadow's record is a self-repair attempt.
	if err := b.BeginSwap(ctx, "coder", SwapIntent{Kind: SwapKindReload, By: "sentinel"}); err != nil {
		t.Fatal(err)
	}
	_ = b.EndSwap(ctx, "coder")
	if err := b.LogWouldRelaunch(ctx, "tok", SwapEvent{Role: "coder", By: "dispatch", Episode: "3:3"}); err != nil {
		t.Fatal(err)
	}
	now, _ := b.ServerTime(ctx)
	past, err := b.SelfRepairPast(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := past["coder"]
	if len(c.Attempts) != 2 || !c.Failed["2:2"] || c.Failed["1:1"] || c.CrashLoop {
		t.Fatalf("coder %+v", c)
	}
	for _, at := range c.Attempts {
		if d := now.Sub(at); d < 0 || d > time.Minute {
			t.Fatalf("attempt at %v, now %v", at, now)
		}
	}
	if !c.Attempts[0].Before(c.Attempts[1]) && !c.Attempts[0].Equal(c.Attempts[1]) {
		t.Fatalf("attempts not oldest first: %v", c.Attempts)
	}
	if _, ok := past["foureyes"]; ok {
		t.Fatalf("foureyes has no past: %+v", past["foureyes"])
	}

	// The crash loop: marked once (the escalation is sent once), kept until reset.
	if fresh, err := b.MarkCrashLoop(ctx, "other", "coder"); !errors.Is(err, ErrNotHolder) || fresh {
		t.Fatalf("another token: %v %v", fresh, err)
	}
	if fresh, err := b.MarkCrashLoop(ctx, "tok", "coder"); err != nil || !fresh {
		t.Fatalf("first mark: %v %v", fresh, err)
	}
	if fresh, err := b.MarkCrashLoop(ctx, "tok", "coder"); err != nil || fresh {
		t.Fatalf("second mark: %v %v", fresh, err)
	}
	if past, _ := b.SelfRepairPast(ctx); !past["coder"].CrashLoop {
		t.Fatalf("crash loop not read: %+v", past["coder"])
	}

	// The reset (agentbus self-repair reset): the crash loop cleared, and what
	// came before it no longer counts, neither attempts nor failed episodes.
	if was, err := b.ResetSelfRepair(ctx, "coder"); err != nil || !was {
		t.Fatalf("reset: %v %v", was, err)
	}
	if was, err := b.ResetSelfRepair(ctx, "coder"); err != nil || was {
		t.Fatalf("second reset: %v %v", was, err)
	}
	if _, err := b.ResetSelfRepair(ctx, "Bad Role"); err == nil {
		t.Fatal("an invalid role reset")
	}
	past, _ = b.SelfRepairPast(ctx)
	if c = past["coder"]; c.CrashLoop || len(c.Attempts) != 0 || len(c.Failed) != 0 {
		t.Fatalf("after reset %+v", c)
	}
	attempt("coder", "c", "4:4", SwapFailed)
	if past, _ = b.SelfRepairPast(ctx); len(past["coder"].Attempts) != 1 || !past["coder"].Failed["4:4"] {
		t.Fatalf("after reset, a new attempt: %+v", past["coder"])
	}
	// reviewer-codex (7a): the budget outlives the ledger's window. 1100
	// unrelated entries trim the ledger past coder's begin; the attempt
	// still counts.
	for i := 0; i < 1100; i++ {
		if err := b.LogSwap(ctx, SwapEvent{Role: "foureyes", Event: SwapWarning, By: "master"}); err != nil {
			t.Fatal(err)
		}
	}
	if log, _ := b.SwapLog(ctx, "coder", 0); len(log) != 0 {
		t.Fatalf("the ledger window still holds coder: %d entries", len(log))
	}
	if past, _ = b.SelfRepairPast(ctx); len(past["coder"].Attempts) != 1 || !past["coder"].Failed["4:4"] {
		t.Fatalf("past lost with the ledger window: %+v", past["coder"])
	}
	// A refused begin or finish counts nothing.
	if err := b.BeginSelfRepair(ctx, "other", "coder", "d", SwapEvent{By: "dispatch", Episode: "5:5"}); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "d", SwapEvent{By: "dispatch", Episode: "5:5"}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "e", SwapEvent{By: "dispatch", Episode: "5:5"}); !errors.Is(err, ErrSwapInProgress) {
		t.Fatal(err)
	}
	if err := b.FinishSelfRepair(ctx, "tok", "wrong", SwapEvent{Role: "coder", Event: SwapFailed, Episode: "5:5"}); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatal(err)
	}
	if past, _ = b.SelfRepairPast(ctx); len(past["coder"].Attempts) != 2 || past["coder"].Failed["5:5"] {
		t.Fatalf("a refused begin or finish counted: %+v", past["coder"])
	}
	if c, k := ClassOf(b.Project(), SelfRepairKey(b.Project())); c != KeyRecord || k != "selfrepair" {
		t.Fatalf("selfrepair key class %v %q", c, k)
	}
}

// The attempts are pruned by age, never by a count below a day's budget
// (reviewer-codex, 7a round 2): dispatch.self_repair_per_day takes any
// positive number, so 70 attempts within 24 h all count; an attempt older
// than 24 h is dropped at the next begin.
func TestSelfRepairAttemptsKeptForTheDayRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "coder", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	now, _ := b.ServerTime(ctx)
	old := now.Add(-25 * time.Hour).UnixMilli()
	recent := now.Add(-23 * time.Hour).UnixMilli()
	b.r.HSet(ctx, SelfRepairKey(b.project), "attempts:coder", strconv.FormatInt(old, 10)+","+strconv.FormatInt(recent, 10))
	for i := 0; i < 70; i++ {
		nonce := "n" + strconv.Itoa(i)
		if err := b.BeginSelfRepair(ctx, "tok", "coder", nonce, SwapEvent{By: "dispatch", Episode: "1:1"}); err != nil {
			t.Fatal(err)
		}
		if err := b.FinishSelfRepair(ctx, "tok", nonce, SwapEvent{Role: "coder", Event: SwapRelaunched, By: "dispatch", Episode: "1:1"}); err != nil {
			t.Fatal(err)
		}
	}
	past, err := b.SelfRepairPast(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := past["coder"].Attempts
	if len(a) != 71 { // the 23-hour-old one and the 70 new ones; the 25-hour-old one dropped
		t.Fatalf("%d attempts kept, want 71", len(a))
	}
	if !a[0].Equal(time.UnixMilli(recent)) {
		t.Fatalf("oldest kept %v, want the 23-hour-old one", a[0])
	}
}
