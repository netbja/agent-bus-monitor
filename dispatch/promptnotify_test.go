package dispatch

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// promptLoop is a dispatcher holding the lease whose last observation shows
// coder (pid 10, pane w2:pA) blocked on its client's prompt since `since`,
// read at `at`.
func promptLoop(t *testing.T, b *bus.Bus, token string, desk *fakeDesktop, since, at int64) *loop {
	t.Helper()
	ok, err := b.AcquireDispatch(context.Background(), token, time.Minute)
	if err == nil && !ok { // a second loop of the same holder
		ok, err = b.RenewDispatch(context.Background(), token, time.Minute)
	}
	if err != nil || !ok {
		t.Fatalf("lease %s = %v, %v", token, ok, err)
	}
	l := &loop{b: b, token: token, last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Prompt: PromptConfig{Grace: 30 * time.Second, Notify: desk.Notify}}}
	l.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: since, At: at})
	return l
}

// observeAt replaces the last observation of agent, as a pass would.
func (l *loop) observeAt(agent string, o bus.Observation) {
	if l.obs == nil {
		l.obs = map[string]Observation{}
	}
	l.obs[agent] = o
	l.obsFresh = true
}

var coderSnap = map[string]bus.AgentSnapshot{"coder": {State: "working", PID: 10, Pane: "w2:pA"}}

// One desktop notification per episode (the observation's pid and since);
// the same episode read again is silent, a new since rings again, and the
// notification names project, agent and pane. Nothing is written to a stream.
func TestPromptNotifyOncePerEpisode(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "prompt-holder", desk, 1000, 1000+31_000)
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	want := "agentbus " + b.Project() + ": " + b.Project() + ": coder waits on a prompt in its pane (w2:pA)"
	if s := desk.Sent(); len(s) != 1 || s[0] != want {
		t.Fatalf("desktop: %q, want %q", s, want)
	}
	l.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: 1000, At: 1000 + 46_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 1 {
		t.Fatalf("same episode rang again: %q", s)
	}
	// Answered, then a new prompt: a new since is a new episode.
	l.observeAt("coder", bus.Observation{State: "working", PID: 10, Since: 50_000, At: 50_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if marks, _ := b.QuotaMarks(ctx); marks[promptMark("coder")] != "" {
		t.Fatalf("an ended episode keeps its mark: %v", marks)
	}
	l.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: 90_000, At: 125_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 2 {
		t.Fatalf("a new episode did not ring: %q", s)
	}
	for _, k := range []string{"cmd", "notify"} {
		if n := len(entries(t, r, b, k)); n != 0 {
			t.Fatalf("%d %s entries written: the notification is desktop only", n, k)
		}
	}
	// A dispatcher that lost the lease rings nothing.
	l2 := &loop{b: b, token: "not-the-holder", last: map[string]string{}, cfg: l.cfg}
	l2.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: 200_000, At: 240_000})
	if err := l2.promptNotices(ctx, coderSnap); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("a non-holder: %v", err)
	}
	if s := desk.Sent(); len(s) != 2 {
		t.Fatalf("a non-holder rang: %q", s)
	}
}

// The grace: a prompt answered within it never rings; one still there once it
// has passed does; a grace of 0 turns the notification off.
func TestPromptNotifyGrace(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "grace-holder", desk, 1000, 1000+15_000)
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("rang within the grace: %q", s)
	}
	l.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: 1000, At: 1000 + 30_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 1 {
		t.Fatalf("did not ring once the grace passed: %q", s)
	}
	off := promptLoop(t, b, "grace-holder", desk, 5000, 5000+600_000)
	off.cfg.Prompt.Grace = 0
	if err := off.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 1 {
		t.Fatalf("rang with the notification off: %q", s)
	}
}

// The prompt flag's suppressions, plus a swap: an agent that declares blocked
// or waiting, has an open ask, is being swapped, or whose observation is of
// another process, or another state, is not rung for.
func TestPromptNotifySuppressions(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if _, _, err := b.Ask(ctx, bus.AskDef{From: "asker", Title: "T", Questions: []bus.AskQuestion{
		{ID: "d", Header: "Date", Question: "When?", Options: []bus.AskOption{{Label: "Mon"}}}}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, bus.SwapKey(b.Project(), "swapped"), "intent", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "supp-holder", desk, 1000, 60_000)
	snaps := map[string]bus.AgentSnapshot{}
	for name, s := range map[string]bus.AgentSnapshot{
		"blocker": {State: "blocked"}, "waiter": {State: "waiting"}, "asker": {State: "idle"},
		"swapped": {State: "working"}, "replaced": {State: "working", PID: 11}, "idle": {State: "idle"},
	} {
		if s.PID == 0 {
			s.PID = 10
		}
		s.Pane = "w2:p" + name
		snaps[name] = s
		state := "blocked"
		if name == "idle" {
			state = "idle"
		}
		l.observeAt(name, bus.Observation{State: state, PID: 10, Since: 1000, At: 60_000})
	}
	delete(l.obs, "coder")
	if err := l.promptNotices(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("rang for a suppressed agent: %q", s)
	}
}

// During a project pause nobody is rung for; the episode rings once it ends.
func TestPromptNotifyWaitsForThePause(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "pause-prompt", desk, 1000, 60_000)
	l.rt = newFakeRT(alwaysReady)
	l.cfg.Horizon = bus.DefaultHorizon
	registerSession(t, b, "coder", "w2:pA", "")
	if _, err := b.SetPause(ctx, "pilot", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	l.obs["coder"] = bus.Observation{State: "blocked", PID: testPID, Since: 1000, At: 60_000}
	if err := l.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("rang during the pause: %q", s)
	}
	if _, err := b.Resume(ctx, "pilot", ""); err != nil {
		t.Fatal(err)
	}
	if err := l.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 1 || !strings.Contains(s[0], "coder waits on a prompt in its pane (w2:pA)") {
		t.Fatalf("after the pause: %q", s)
	}
}

// A runner that fails, or none (no notify-send), is a logged note: the pass
// goes on, and the episode is marked, so it is not retried every pass.
func TestPromptNotifyRunnerFailure(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	logs := &syncBuf{}
	l := promptLoop(t, b, "fail-holder", desk, 1000, 60_000)
	l.cfg.Log = logs
	calls := 0
	l.cfg.Prompt.Notify = func(context.Context, string, string) error { calls++; return errors.New("no display") }
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatalf("a failed runner failed the pass: %v", err)
	}
	if calls != 1 || !strings.Contains(logs.String(), "no display") {
		t.Fatalf("calls %d, log %q", calls, logs.String())
	}
	l.observeAt("coder", bus.Observation{State: "blocked", PID: 10, Since: 1000, At: 75_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil || calls != 1 {
		t.Fatalf("retried the same episode: calls %d, %v", calls, err)
	}

	none := promptLoop(t, b, "fail-holder", desk, 2000, 60_000)
	none.cfg.Log = logs
	none.cfg.Prompt.Notify = nil
	if err := none.promptNotices(ctx, coderSnap); err != nil {
		t.Fatalf("no runner failed the pass: %v", err)
	}
	if !strings.Contains(logs.String(), "notify-send") {
		t.Fatalf("no note for a missing runner: %q", logs.String())
	}
}
