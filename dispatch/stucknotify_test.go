package dispatch

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// stuckLoop is a dispatcher holding the lease, prompts off, stuck after 3
// minutes: a stuck screen rings on its own, whatever the prompt grace.
func stuckLoop(t *testing.T, b *bus.Bus, token string, desk *fakeDesktop) *loop {
	t.Helper()
	ok, err := b.AcquireDispatch(context.Background(), token, time.Minute)
	if err == nil && !ok { // a second loop of the same holder
		ok, err = b.RenewDispatch(context.Background(), token, time.Minute)
	}
	if err != nil || !ok {
		t.Fatalf("lease %s = %v, %v", token, ok, err)
	}
	return &loop{b: b, token: token, last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Prompt: PromptConfig{StuckAfter: 3 * time.Minute, Notify: desk.Notify}}}
}

func unknownScreen(hash string, since, at int64) bus.Observation {
	return bus.Observation{State: "unknown", PID: 10, Since: since, At: at, ScreenHash: hash, ScreenSince: since}
}

// agent-screen-blocked §3, the person's Q3 and Q6: an unrecognised screen
// unchanged for stuck_after rings once per episode (pid and ScreenSince),
// with words that never say prompt; not before; a new hash is a new
// episode; a recognised state ends it and drops its mark.
func TestStuckNotifyOncePerEpisode(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := stuckLoop(t, b, "stuck-holder", desk)
	l.observeAt("coder", unknownScreen("h1", 1000, 1000+179_000))
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("rang before stuck_after: %q", s)
	}
	l.observeAt("coder", unknownScreen("h1", 1000, 1000+181_000))
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	want := "agentbus " + b.Project() + ": " + b.Project() + ": coder's screen has not changed for 3m and is not a prompt agentbus knows (w2:pA)"
	if s := desk.Sent(); len(s) != 1 || s[0] != want {
		t.Fatalf("desktop %q, want %q", s, want)
	}
	if strings.Contains(desk.Sent()[0], "waits on a prompt") {
		t.Fatal("a stuck screen is rung as a prompt")
	}
	l.observeAt("coder", unknownScreen("h1", 1000, 1000+300_000))
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 1 {
		t.Fatalf("the same episode rang again: %q", s)
	}
	// The screen changed and stayed again: a new episode.
	l.observeAt("coder", unknownScreen("h2", 400_000, 400_000+200_000))
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 2 {
		t.Fatalf("a new episode did not ring: %q", s)
	}
	// Recognised again: the episode's mark goes.
	l.observeAt("coder", bus.Observation{State: "idle", PID: 10, Since: 700_000, At: 700_000, ScreenHash: "h3", ScreenSince: 700_000})
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if marks, _ := b.QuotaMarks(ctx); marks[stuckMark("coder")] != "" {
		t.Fatalf("an ended episode keeps its mark: %v", marks)
	}
}

// A prompt is not stuck: it is prompt-notify's, never rung with the stuck
// words; and stuck_after 0 turns the ring off.
func TestStuckNotifyIsNotAPrompt(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := stuckLoop(t, b, "stuck-prompt-holder", desk)
	o := unknownScreen("h1", 1000, 1000+600_000)
	o.State = "blocked"
	l.observeAt("coder", o)
	if err := l.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	for _, s := range desk.Sent() {
		if strings.Contains(s, "has not changed") {
			t.Fatalf("a prompt rang as stuck: %q", s)
		}
	}
	off := stuckLoop(t, b, "stuck-prompt-holder", desk)
	off.cfg.Prompt.StuckAfter = 0
	off.observeAt("coder", unknownScreen("h1", 1000, 1000+600_000))
	if err := off.promptNotices(ctx, coderSnap); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("stuck_after 0 rang: %q", s)
	}
}

// prompt-notify's suppressions hold for stuck: a declared blocked or
// waiting, an open ask, a swap, another process; and a screen with no hash
// (not settled, or a client agentbus does not read) is never stuck.
func TestStuckNotifySuppressions(t *testing.T) {
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
	l := stuckLoop(t, b, "stuck-supp-holder", desk)
	snaps := map[string]bus.AgentSnapshot{}
	for name, s := range map[string]bus.AgentSnapshot{
		"blocker": {State: "blocked"}, "waiter": {State: "waiting"}, "asker": {State: "idle"},
		"swapped": {State: "working"}, "replaced": {State: "working", PID: 11}, "nohash": {State: "idle"},
	} {
		if s.PID == 0 {
			s.PID = 10
		}
		s.Pane = "w2:p" + name
		snaps[name] = s
		o := unknownScreen("h1", 1000, 1000+600_000)
		if name == "nohash" {
			o.ScreenHash, o.ScreenSince = "", 0
		}
		l.observeAt(name, o)
	}
	if err := l.promptNotices(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	if s := desk.Sent(); len(s) != 0 {
		t.Fatalf("rang for a suppressed agent: %q", s)
	}
}

// The observe pass carries ScreenSince as it carries ScreenRepeats: the same
// pid and hash keep the first reading's time; a new hash or pid starts it
// again; a pass with no hash (not settled) has none.
func TestCarryScreen(t *testing.T) {
	prev := bus.Observation{PID: 10, ScreenHash: "h1", ScreenRepeats: 4, ScreenSince: 1000}
	for _, c := range []struct {
		name    string
		o       bus.Observation
		hasPrev bool
		since   int64
		repeats int
	}{
		{"same pid and hash", bus.Observation{PID: 10, ScreenHash: "h1", At: 9000}, true, 1000, 5},
		{"new hash", bus.Observation{PID: 10, ScreenHash: "h2", At: 9000}, true, 9000, 1},
		{"new pid", bus.Observation{PID: 11, ScreenHash: "h1", At: 9000}, true, 9000, 1},
		{"first reading", bus.Observation{PID: 10, ScreenHash: "h1", At: 9000}, false, 9000, 1},
		{"not settled", bus.Observation{PID: 10, At: 9000}, true, 0, 0},
	} {
		o := c.o
		carryScreen(&o, prev, c.hasPrev)
		if o.ScreenSince != c.since || o.ScreenRepeats != c.repeats {
			t.Errorf("%s: since %d repeats %d, want %d %d", c.name, o.ScreenSince, o.ScreenRepeats, c.since, c.repeats)
		}
	}
}
