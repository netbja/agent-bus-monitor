package dispatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestWakeTextExact(t *testing.T) {
	got := WakeText("demo", "coder", "a1b2c3", 3)
	want := "[agentbus#a1b2c3] demo: 3 message(s) waiting for coder. Run: agentbus --project demo subscribe --drain coder"
	if got != want {
		t.Fatalf("WakeText =\n%q\nwant\n%q", got, want)
	}
}

func TestDefaultConfigPinnedValues(t *testing.T) {
	c := DefaultConfig()
	if c.Horizon != 24*time.Hour || c.Retry != 60*time.Second || c.EscalateAfter != 10*time.Minute ||
		c.Tick != 5*time.Second || c.Lease != 15*time.Second || c.Renew != 5*time.Second ||
		c.MinPTTL != 5*time.Second || c.MaxTries != 3 || c.EscalateTo != "master" || c.Log == nil {
		t.Fatalf("DefaultConfig = %+v", c)
	}
}

var nonceRE = regexp.MustCompile(`^\[agentbus#([0-9a-f]{6})\] `)

func TestRunWakesUnarmedIdleAgent(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())

	waitFor(t, 3*time.Second, "a prompt", func() bool { return len(rt.Prompts()) == 1 })
	p := rt.Prompts()[0]
	m := nonceRE.FindStringSubmatch(p.Text)
	if p.Pane != "p-coder" || m == nil || m[1] != p.Nonce {
		t.Fatalf("prompt = %+v, want pane p-coder with its nonce marker", p)
	}
	if want := WakeText(b.Project(), "coder", p.Nonce, 1); p.Text != want {
		t.Fatalf("text = %q, want %q", p.Text, want)
	}
	waitFor(t, 2*time.Second, "prompted record", func() bool {
		w, ok := wakeOf(t, b, "coder")
		return ok && w.State == "prompted" && w.Tries == 1 && w.At != 0 && w.PID == testPID
	})
}

func TestRunSkipsArmedAgent(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "coder", "p-coder")
	r.Set(context.Background(), bus.ReceiverKey(b.Project(), "coder"), "someone", 0)
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())

	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 for an armed agent", n)
	}
	if n := rt.Inspections("p-coder"); n != 0 {
		t.Fatalf("inspections = %d, want 0 (armed agents are not inspected)", n)
	}
}

func TestRunBurstOnePrompt(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	for i := 0; i < 10; i++ {
		send(t, b, "coder", fmt.Sprintf("msg %d", i))
	}
	waitFor(t, 3*time.Second, "a prompt", func() bool { return len(rt.Prompts()) >= 1 })
	for i := 0; i < 5; i++ {
		send(t, b, "coder", "late")
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts = %d, want exactly 1 per window", n)
	}
}

func TestRunCrashAfterPromptNoSecondPromptInWindow(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	// Wider than a Run's shutdown (its cmd tail blocks up to 1 s).
	cfg.Retry = 2500 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	withHooks(t, hooks{afterPrompt: cancel})
	first := startWith(t, ctx, cancel, b, rt, cfg)
	first.wait(t, 3*time.Second)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts after crash = %d, want 1", n)
	}
	if w, _ := wakeOf(t, b, "coder"); w.State != "prompting" {
		t.Fatalf("state = %q, want prompting (crashed before PutWake)", w.State)
	}

	testHooks = hooks{}
	start(t, b, rt, cfg)
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts within the window after restart = %d, want 1", n)
	}
	waitFor(t, 4*time.Second, "the next window's prompt", func() bool { return len(rt.Prompts()) == 2 })
	ps := rt.Prompts()
	if gap := ps[1].At.Sub(ps[0].At); gap < 2400*time.Millisecond {
		t.Fatalf("second prompt %v after the first, want ≥ the retry window", gap)
	}
	if w, _ := wakeOf(t, b, "coder"); w.Tries != 2 {
		t.Fatalf("tries = %d, want 2", w.Tries)
	}
}

func TestRunLeaseLostAfterReserveNoPrompt(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	withHooks(t, hooks{afterReserve: func() {
		r.Del(context.Background(), bus.DispatchKey(b.Project()))
	}})
	run := start(t, b, rt, fastConfig())
	if err := run.wait(t, 3*time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Run = %v, want ErrLeaseLost", err)
	}
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 after losing the lease", n)
	}
}

func TestRunReadinessChangedAtRecheckDefers(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(func(_ string, n int) Inspection {
		in := readyIn()
		in.Focused = n >= 2
		return in
	})
	start(t, b, rt, fastConfig())
	waitFor(t, 3*time.Second, "deferred record", func() bool {
		w, ok := wakeOf(t, b, "coder")
		return ok && w.State == "deferred" && strings.Contains(w.Reason, "focused")
	})
	time.Sleep(100 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 (focused at the re-check)", n)
	}
	if w, _ := wakeOf(t, b, "coder"); w.At != 0 || w.Tries != 0 {
		t.Fatalf("record = %+v, want no reservation", w)
	}
}

func errIn(string, int) Inspection {
	return Inspection{Err: errors.New("herdr socket absent")}
}

func TestRunCrashDuringEscalationResendsOnce(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(errIn)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	withHooks(t, hooks{afterEscalatePending: cancel})
	first := startWith(t, ctx, cancel, b, rt, cfg)
	first.wait(t, 3*time.Second)
	if w, _ := wakeOf(t, b, "coder"); w.Escalated != "pending" {
		t.Fatalf("escalated = %q, want pending", w.Escalated)
	}
	if n := len(escalations(t, r, b, "coder")); n != 0 {
		t.Fatalf("escalations before restart = %d, want 0", n)
	}

	testHooks = hooks{}
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "escalated=true", func() bool {
		w, _ := wakeOf(t, b, "coder")
		return w.Escalated == "true"
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(escalations(t, r, b, "coder")); n != 1 {
		t.Fatalf("escalations = %d, want exactly 1", n)
	}
	if n := len(notifies(t, r, b, "coder")); n != 1 {
		t.Fatalf("notifies = %d, want exactly 1", n)
	}
}

func TestRunSecondDispatcherExits(t *testing.T) {
	b, _ := dialRun(t)
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	waitFor(t, 2*time.Second, "lease", func() bool {
		ok, _ := b.DispatchAlive(context.Background())
		return ok
	})
	second := start(t, b, rt, fastConfig())
	if err := second.wait(t, 2*time.Second); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run = %v, want ErrAlreadyRunning", err)
	}
}

func TestRunLeaseLapseWakes(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "coder", "p-coder")
	r.Set(context.Background(), bus.ReceiverKey(b.Project(), "coder"), "someone", 400*time.Millisecond)
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	time.Sleep(200 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts while armed = %d, want 0", n)
	}
	waitFor(t, 3*time.Second, "prompt after the receiver lease lapsed", func() bool { return len(rt.Prompts()) == 1 })
}

// requesterPrompts waits for exactly one prompt, to master.
func requesterPrompted(t *testing.T, rt *fakeRT) {
	t.Helper()
	waitFor(t, 3*time.Second, "requester prompt", func() bool { return len(rt.Prompts()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	ps := rt.Prompts()
	if len(ps) != 1 || ps[0].Pane != "p-master" || !strings.Contains(ps[0].Text, "waiting for master.") {
		t.Fatalf("prompts = %+v, want one for master", ps)
	}
}

func TestRunRequestDoneWakesRequester(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "master", "p-master")
	if _, err := b.Request(ctx, "t1", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "t1", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "t1", "coder", "done it"); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	requesterPrompted(t, rt)
}

func TestRunRequestBlockWakesRequester(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "master", "p-master")
	if _, err := b.Request(ctx, "t1", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "t1", "coder", "need credentials"); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	requesterPrompted(t, rt)
}

func TestRunStaleOnlyNoWake(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "old news")
	time.Sleep(150 * time.Millisecond)
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.Horizon = 100 * time.Millisecond
	start(t, b, rt, cfg)
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 for a stale-only obligation", n)
	}
	if _, ok := wakeOf(t, b, "coder"); ok {
		t.Fatal("a stale-only obligation wrote a wake record")
	}
}

func TestRunNoWakeEntryNoPrompt(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	if _, err := b.CmdNoWake(context.Background(), "peer", "coder", bus.CmdDirective, "", "fyi"); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 for wake=no", n)
	}
}

func hasGroup(t *testing.T, b *bus.Bus, name string) bool {
	names, err := b.GroupNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestRunCreatesGroupForUngroupedTarget(t *testing.T) {
	b, _ := dialRun(t)
	early := send(t, b, "ghost", "before registration")
	if hasGroup(t, b, "ghost") {
		t.Fatal("ghost already has a group")
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	waitFor(t, 3*time.Second, "ghost group", func() bool { return hasGroup(t, b, "ghost") })
	o, err := b.Obligation(context.Background(), "ghost", bus.DefaultHorizon)
	if err != nil || o.OldestID != early {
		t.Fatalf("obligation = %+v (%v), want the group to capture %s", o, err, early)
	}
	waitFor(t, 2*time.Second, "deferred record", func() bool {
		w, ok := wakeOf(t, b, "ghost")
		return ok && w.State == "deferred" && strings.Contains(w.Reason, "no snapshot")
	})
}

func TestRunGapNotifiedOnce(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	key := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, key, "gapper", "0").Err(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		r.XAdd(ctx, &redis.XAddArgs{Stream: key, MaxLen: 5, Values: map[string]interface{}{
			"from": "peer", "target": "other", "type": bus.CmdDirective, "ref": "", "command": "x"}})
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	waitFor(t, 3*time.Second, "gap flag", func() bool {
		w, ok := wakeOf(t, b, "gapper")
		return ok && w.GapNotified
	})
	time.Sleep(200 * time.Millisecond)
	if n := len(notifies(t, r, b, "gapper")); n != 1 {
		t.Fatalf("gap notifies = %d, want 1", n)
	}
}

func TestRunEscalationTargetsRoleNotPilot(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	r.Set(ctx, bus.PilotKey(b.Project()), "captain", time.Minute)
	send(t, b, "ghost", "anyone home?")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "escalation", func() bool { return len(escalations(t, r, b, "ghost")) == 1 })
	e := escalations(t, r, b, "ghost")[0]
	if e.Values["target"] != "master" || e.Values["from"] != "dispatch" {
		t.Fatalf("escalation = %v, want from dispatch to master", e.Values)
	}
	body, _ := e.Values["command"].(string)
	if !regexp.MustCompile(`^ghost: .+; 1 waiting, oldest \d+-\d+$`).MatchString(body) {
		t.Fatalf("escalation body = %q", body)
	}
}

func TestRunSelfEscalationNotifiesOnly(t *testing.T) {
	b, r := dialRun(t)
	send(t, b, "master", "wake up")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "escalated", func() bool {
		w, _ := wakeOf(t, b, "master")
		return w.Escalated == "true"
	})
	time.Sleep(200 * time.Millisecond)
	if n := len(escalations(t, r, b, "master")); n != 0 {
		t.Fatalf("self-escalation cmds = %d, want 0", n)
	}
	if n := len(notifies(t, r, b, "master")); n != 1 {
		t.Fatalf("notifies = %d, want 1", n)
	}
}

func TestRunRuntimeErrorDefersAndEscalatesOnce(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(errIn)
	cfg := fastConfig()
	cfg.EscalateAfter = 400 * time.Millisecond
	start(t, b, rt, cfg)
	// Escalated == "" tells the pre-escalation defer apart from the record
	// after escalation (State stays deferred, reason "…, escalating").
	waitFor(t, 2*time.Second, "deferred on inspection error", func() bool {
		w, ok := wakeOf(t, b, "coder")
		return ok && w.State == "deferred" && w.Escalated == "" && strings.Contains(w.Reason, "inspection error")
	})
	if n := len(escalations(t, r, b, "coder")); n != 0 {
		t.Fatalf("escalated before T: %d", n)
	}
	waitFor(t, 3*time.Second, "escalation", func() bool { return len(escalations(t, r, b, "coder")) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if n := len(escalations(t, r, b, "coder")); n != 1 {
		t.Fatalf("escalations = %d, want 1", n)
	}
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0", n)
	}
}

func TestRunUnregisteredTargetEscalatesOnce(t *testing.T) {
	b, r := dialRun(t)
	send(t, b, "ghost", "hello?")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "escalation", func() bool { return len(escalations(t, r, b, "ghost")) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if n := len(escalations(t, r, b, "ghost")); n != 1 {
		t.Fatalf("escalations = %d, want 1", n)
	}
	if n := len(notifies(t, r, b, "ghost")); n != 1 {
		t.Fatalf("notifies = %d, want 1", n)
	}
}

func TestRunDoesNotEscalateToHumanSender(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	if _, err := b.HumanCmd(ctx, "bernard", "coder", "please check this"); err != nil {
		t.Fatal(err)
	}
	send(t, b, "bernard", "this target is a person")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	time.Sleep(500 * time.Millisecond)
	if got := escalations(t, r, b, "bernard"); len(got) != 0 {
		t.Fatalf("human identity got escalated: %+v", got)
	}
	if got := notifies(t, r, b, "bernard"); len(got) != 0 {
		t.Fatalf("human identity got dispatcher notify: %+v", got)
	}
	if _, ok := wakeOf(t, b, "bernard"); ok {
		t.Fatal("human identity got an agent wake record")
	}
}

// Controller ruling 2: after progress the escalation that follows refreshes the
// progress marks, so the next tick is quiet instead of re-escalating forever.
func TestRunProgressThenEscalateOnceMore(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	first := send(t, b, "coder", "one")
	send(t, b, "coder", "two")
	rt := newFakeRT(errIn)
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "first escalation", func() bool {
		w, _ := wakeOf(t, b, "coder")
		return w.Escalated == "true"
	})

	// Progress: the group's cursor moves past the first entry in one step (a
	// read+ack seen half-done by a tick would count as two progress events).
	if err := r.XGroupSetID(ctx, bus.StreamKey(b.Project(), "cmd"), "coder", first).Err(); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 3*time.Second, "second escalation", func() bool { return len(escalations(t, r, b, "coder")) == 2 })
	time.Sleep(300 * time.Millisecond)
	if n := len(escalations(t, r, b, "coder")); n != 2 {
		t.Fatalf("escalations = %d, want 2 (one per stall, not one per tick)", n)
	}
}

// Controller ruling 1: rule 1 clears the record but keeps At, so a new message
// right after a drained one cannot bypass the retry window.
func TestRunClearedRecordKeepsWindow(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	one := send(t, b, "coder", "one")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	waitFor(t, 3*time.Second, "prompt", func() bool { return len(rt.Prompts()) == 1 })
	waitFor(t, 2*time.Second, "prompted", func() bool {
		w, _ := wakeOf(t, b, "coder")
		return w.State == "prompted"
	})
	before, _ := wakeOf(t, b, "coder")

	if err := r.XGroupSetID(ctx, bus.StreamKey(b.Project(), "cmd"), "coder", one).Err(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "cleared record", func() bool {
		w, ok := wakeOf(t, b, "coder")
		return ok && w.State == "clear"
	})
	w, _ := wakeOf(t, b, "coder")
	if w.At != before.At || w.Tries != 0 || w.PID != testPID {
		t.Fatalf("cleared = %+v, want At %d kept, tries 0, pid kept", w, before.At)
	}
	send(t, b, "coder", "two")
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts = %d, want 1 (still inside the retry window)", n)
	}
}

func TestRunProjectDeletedStops(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	send(t, b, "ghost", "hello?")
	rt := newFakeRT(errIn)
	run := start(t, b, rt, fastConfig())
	// Both agents' first writes must have landed: DeleteProject is SCAN then
	// DEL, so a write in that gap would survive the delete.
	waitFor(t, 3*time.Second, "both wake records", func() bool {
		_, okc := wakeOf(t, b, "coder")
		_, okg := wakeOf(t, b, "ghost")
		return okc && okg
	})
	time.Sleep(100 * time.Millisecond) // steady: defers no longer rewrite
	if _, err := bus.DeleteProject(ctx, r, b.Project()); err != nil {
		t.Fatal(err)
	}
	if err := run.wait(t, 3*time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Run = %v, want ErrLeaseLost", err)
	}
	time.Sleep(100 * time.Millisecond)
	keys, err := bus.ProjectKeys(ctx, r, b.Project())
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys after delete = %v (%v), want none", keys, err)
	}
}

func TestRunLargeBacklogOnePromptThenDrainInOrder(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	sent := make([]string, 0, 1000)
	for i := 0; i < 1000; i++ {
		sent = append(sent, send(t, b, "coder", fmt.Sprintf("job %d", i)))
	}
	rt := newFakeRT(alwaysReady)
	run := start(t, b, rt, fastConfig())
	waitFor(t, 5*time.Second, "a prompt", func() bool { return len(rt.Prompts()) >= 1 })
	time.Sleep(200 * time.Millisecond)
	run.stop(t)
	ps := rt.Prompts()
	if len(ps) != 1 || !strings.Contains(ps[0].Text, " 1000 message(s) waiting for coder.") {
		t.Fatalf("prompts = %+v, want one announcing 1000 messages", ps)
	}
	var got []string
	n, err := b.DrainCmd(ctx, "coder", "c", bus.DefaultHorizon, func(e bus.Event) error {
		got = append(got, e.ID)
		return nil
	})
	if err != nil || n != 1000 || len(got) != 1000 {
		t.Fatalf("DrainCmd = %d, %v (got %d ids), want 1000", n, err, len(got))
	}
	for i := range got {
		if got[i] != sent[i] {
			t.Fatalf("drain[%d] = %s, want %s (ascending, nothing lost)", i, got[i], sent[i])
		}
	}
}

// OnLease tells a supervisor (agentbus service run) when this dispatcher holds
// the lease, so it can say "ready" rather than merely "running".
func TestRunReportsTheLease(t *testing.T) {
	b, _ := dialRun(t)
	var mu sync.Mutex
	var seen []bool
	cfg := fastConfig()
	cfg.OnLease = func(held bool) { mu.Lock(); seen = append(seen, held); mu.Unlock() }
	r := start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 2*time.Second, "lease reported", func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 && seen[0] })
	r.stop(t)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[1] {
		t.Fatalf("OnLease calls = %v, want [true false]", seen)
	}
}

// A paused project wakes nobody; when the pause is due the dispatcher ends it
// with one resume directive, which then wakes its target like any directive.
func TestRunPauseHoldsWakesThenResumes(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PauseKey(b.Project())) })
	register(t, b, "coder", "p-coder")
	register(t, b, "master", "p-master")
	if _, err := b.SetPause(ctx, "master", "master", "quota", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	time.Sleep(400 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("%d prompts while paused", n)
	}
	r.Set(ctx, bus.PauseKey(b.Project()), `{"until":1,"reason":"quota","by":"master","notify":"master","at":1}`, 0)
	waitFor(t, 3*time.Second, "the resume and the wakes", func() bool {
		panes := map[string]bool{}
		for _, p := range rt.Prompts() {
			panes[p.Pane] = true
		}
		return panes["p-master"] && panes["p-coder"]
	})
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatal("pause kept")
	}
}

// v0.15 §14, the incident of 2026-10-16: master's tab blocked by its client's
// menu, escalated to itself (a notify for the person, naming the tab), then,
// once the menu is closed, woken without any delivery in between.
func TestRunBlockedSelfEscalationReArmsWhenTheTabIsBack(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "master", "p-master")
	send(t, b, "master", "resume the team's work")
	var back atomic.Bool
	rt := newFakeRT(func(string, int) Inspection {
		in := readyIn()
		if !back.Load() {
			in.State, in.Ready = "blocked", false
		}
		return in
	})
	cfg := fastConfig()
	cfg.EscalateAfter = 100 * time.Millisecond
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "escalated", func() bool {
		w, _ := wakeOf(t, b, "master")
		return w.Escalated == "true" && w.EscCause == "agent blocked"
	})
	ns := notifies(t, r, b, "master")
	if len(ns) != 1 {
		t.Fatalf("notifies = %d, want 1", len(ns))
	}
	if msg, _ := ns[0].Values["message"].(string); !strings.Contains(msg, "pane p-master") || !strings.Contains(msg, "answer it there") {
		t.Fatalf("notify = %q, want the tab named", msg)
	}
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompted a blocked pane: %d", n)
	}
	back.Store(true)
	waitFor(t, 3*time.Second, "a wake once the tab is back", func() bool { return len(rt.Prompts()) == 1 })
	time.Sleep(200 * time.Millisecond)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts = %d, want 1 (retry window)", n)
	}
}

// v0.15 §6.2: a pane under a swap intent is never prompted; once the intent
// goes, the agent is woken as usual.
func TestRunSwapIntentBlocksPrompting(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "coder", "p-coder")
	if err := b.BeginSwap(ctx, "coder", bus.SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.EscalateAfter = 50 * time.Millisecond
	start(t, b, rt, cfg)
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("prompted under a swap intent: %d", n)
	}
	if w, _ := wakeOf(t, b, "coder"); w.Escalated != "" {
		t.Fatalf("escalated under a swap intent: %+v", w)
	}
	if err := b.EndSwap(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "a wake once the swap is over", func() bool { return len(rt.Prompts()) == 1 })
}
