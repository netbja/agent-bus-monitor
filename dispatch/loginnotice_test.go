package dispatch

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// loginWorld is a dispatcher holding the lease, whose Claude transcripts say
// the login expired at a fixed time.
func loginWorld(t *testing.T, expired *bool) (*loop, *bus.Bus) {
	t.Helper()
	b, _ := dialRun(t)
	if ok, err := b.AcquireDispatch(context.Background(), "holder", time.Minute); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	at := time.Date(2026, 11, 15, 9, 41, 0, 0, time.UTC)
	l := &loop{b: b, token: "holder", last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Login: LoginConfig{Zone: time.UTC, ClaudeExpired: func(time.Time) (time.Time, bool) { return at, *expired }}}}
	return l, b
}

func personNotices(t *testing.T, b *bus.Bus) []bus.Event {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"notify"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []bus.Event
	for _, e := range evs {
		if e.For == bus.ForPerson {
			out = append(out, e)
		}
	}
	return out
}

// login-updates 2.4: an expired Claude login is one notice for the person,
// account-wide (no agent named), at most once a day: a later pass the same
// day sends nothing; the next day (another episode) sends again. No expiry,
// no notice.
func TestLoginPassTellsThePersonOnceADay(t *testing.T) {
	expired := true
	l, b := loginWorld(t, &expired)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		l.loginAt = time.Time{} // past the throttle
		if err := l.loginPass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ns := personNotices(t, b)
	want := "Claude Code login expired (seen 09:41 in a Claude session; the login is the account's, so every Claude agent is affected): run /login in one Claude pane; restart a pane that still fails after it."
	if len(ns) != 1 || ns[0].Message != want || ns[0].From != "dispatch" {
		t.Fatalf("notices %+v", ns)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if marks, err := b.QuotaMarks(ctx); err != nil || marks[loginExpiredMark("claude-code")] != now.In(time.UTC).Format("2006-01-02") {
		t.Fatalf("the episode is not today's date: %v %v", marks, err)
	}
	// Another day: the once-mark holds yesterday's date.
	if err := b.FencedQuotaSet(ctx, "holder", loginExpiredMark("claude-code"), "2000-01-01"); err != nil {
		t.Fatal(err)
	}
	l.loginAt = time.Time{}
	if err := l.loginPass(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(personNotices(t, b)); n != 2 {
		t.Fatalf("%d notices after a new day, want 2", n)
	}
}

func TestLoginPassQuietWhenLoggedIn(t *testing.T) {
	expired := false
	l, b := loginWorld(t, &expired)
	if err := l.loginPass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ns := personNotices(t, b); len(ns) != 0 {
		t.Fatalf("notices %+v", ns)
	}
}

// The transcripts are read at most once per Every, not on every tick.
func TestLoginPassIsThrottled(t *testing.T) {
	expired := false
	l, _ := loginWorld(t, &expired)
	reads := 0
	l.cfg.Login.ClaudeExpired = func(time.Time) (time.Time, bool) { reads++; return time.Time{}, false }
	for i := 0; i < 5; i++ {
		if err := l.loginPass(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Fatalf("%d reads in one minute, want 1", reads)
	}
}

// The pass runs in the dispatcher's loop.
func TestRunTellsAnExpiredLogin(t *testing.T) {
	b, _ := dialRun(t)
	cfg := fastConfig()
	cfg.Login = LoginConfig{ClaudeExpired: func(time.Time) (time.Time, bool) { return time.Now(), true }}
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "the person's notice", func() bool { return len(personNotices(t, b)) == 1 })
}
