package dispatch

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// fakeDesktop records the desktop notifications the alert asks for.
type fakeDesktop struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeDesktop) Notify(_ context.Context, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, title+": "+body)
	return nil
}

func (f *fakeDesktop) Sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// registerSession publishes an idle status whose identity proof names session.
func registerSession(t *testing.T, b *bus.Bus, agent, pane, session string) {
	t.Helper()
	if _, err := b.Status(context.Background(), agent, "idle", "", bus.AgentIdent{Pane: pane, PID: testPID, PIDStart: testStart, Session: session}); err != nil {
		t.Fatalf("Status(%s): %v", agent, err)
	}
}

// contextCmds is every cmd entry the alert wrote, as target|wake|command.
func contextCmds(t *testing.T, r *redis.Client, b *bus.Bus) []string {
	t.Helper()
	var out []string
	for _, m := range entries(t, r, b, "cmd") {
		if m.Values["from"] != dispatchFrom || !strings.Contains(m.Values["command"].(string), "context") {
			continue
		}
		wake, _ := m.Values["wake"].(string)
		out = append(out, m.Values["target"].(string)+"|"+wake+"|"+m.Values["command"].(string))
	}
	return out
}

func alertLoop(b *bus.Bus, token string, read func(string) (quota.ContextReading, bool), desk *fakeDesktop) *loop {
	return &loop{b: b, token: token, last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Context: ContextConfig{Read: read, Alert: 80, MaxAge: 30 * time.Minute, Notify: desk.Notify}}}
}

// At or above the threshold the agent gets one directive, the escalation
// target one wake=no notice and the person one desktop notification, once per
// episode: a reading below the threshold ends the episode, and a new session
// opens another.
func TestContextAlertOncePerEpisode(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "alert-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	var mu sync.Mutex
	pct := map[string]float64{"s1": 85}
	read := func(s string) (quota.ContextReading, bool) {
		mu.Lock()
		defer mu.Unlock()
		p, ok := pct[s]
		return quota.ContextReading{Pct: p, ObservedAt: time.Now().UnixMilli(), Session: s}, ok
	}
	set := func(s string, p float64) { mu.Lock(); pct[s] = p; mu.Unlock() }
	desk := &fakeDesktop{}
	l := alertLoop(b, "alert-holder", read, desk)
	pass := func(snaps map[string]bus.AgentSnapshot) {
		t.Helper()
		if err := l.contextPass(ctx, snaps); err != nil {
			t.Fatal(err)
		}
		if err := l.contextAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	coder := map[string]bus.AgentSnapshot{"coder": {PID: 10, IdentSession: "s1"}}

	pass(coder)
	got := contextCmds(t, r, b)
	if len(got) != 2 ||
		got[0] != "coder||context 85%: at your next stopping point commit WIP and reply to master with a hand-off" ||
		!strings.HasPrefix(got[1], "master|no|") || !strings.Contains(got[1], "coder") || !strings.Contains(got[1], "85%") {
		t.Fatalf("first alert: %q", got)
	}
	if s := desk.Sent(); len(s) != 1 || !strings.Contains(s[0], "coder") || !strings.Contains(s[0], "85%") {
		t.Fatalf("desktop: %q", s)
	}

	set("s1", 92) // same episode: nothing more
	pass(coder)
	if got := contextCmds(t, r, b); len(got) != 2 || len(desk.Sent()) != 1 {
		t.Fatalf("same episode sent again: %q, %q", got, desk.Sent())
	}

	set("s1", 60) // below: the episode ends
	pass(coder)
	set("s1", 81) // above again: a new episode
	pass(coder)
	if got := contextCmds(t, r, b); len(got) != 4 || !strings.HasPrefix(got[2], "coder||context 81%") || len(desk.Sent()) != 2 {
		t.Fatalf("after a drop: %q, %q", got, desk.Sent())
	}

	set("s2", 83) // a new session above the threshold: a new episode
	pass(map[string]bus.AgentSnapshot{"coder": {PID: 10, IdentSession: "s2"}})
	if got := contextCmds(t, r, b); len(got) != 6 || !strings.HasPrefix(got[4], "coder||context 83%") || len(desk.Sent()) != 3 {
		t.Fatalf("new session: %q, %q", got, desk.Sent())
	}

	// A dispatcher that lost the lease writes nothing.
	set("s3", 95)
	l2 := alertLoop(b, "not-the-holder", read, desk)
	l2.ctxFills = map[string]bus.ContextFill{"coder": {Pct: 95, ObservedAt: time.Now().UnixMilli(), Session: "s3", At: time.Now().UnixMilli()}}
	l2.ctxFresh = true
	if err := l2.contextAlerts(ctx); err != bus.ErrNotHolder {
		t.Fatalf("a non-holder: %v", err)
	}
}

// Unknown, aged or under-threshold fills alert nobody; the escalation target
// alerted about itself gets the directive only; 0 turns the alert off.
func TestContextAlertSkipsUnknownAgedAndOff(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "skip-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	now := time.Now().UnixMilli()
	readings := map[string]quota.ContextReading{
		"s-aged":   {Pct: 97, ObservedAt: now - time.Hour.Milliseconds()},
		"s-low":    {Pct: 79.9, ObservedAt: now},
		"s-master": {Pct: 88, ObservedAt: now},
	}
	read := func(s string) (quota.ContextReading, bool) { r, ok := readings[s]; return r, ok }
	snaps := map[string]bus.AgentSnapshot{
		"aged":    {PID: 1, IdentSession: "s-aged"},
		"low":     {PID: 2, IdentSession: "s-low"},
		"unknown": {PID: 3, IdentSession: "s-none"},
		"master":  {PID: 4, IdentSession: "s-master"},
	}
	desk := &fakeDesktop{}
	off := alertLoop(b, "skip-holder", read, desk)
	off.cfg.Context.Alert = 0
	if err := off.contextPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	if err := off.contextAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := contextCmds(t, r, b); len(got) != 0 || len(desk.Sent()) != 0 {
		t.Fatalf("alert off sent %q, %q", got, desk.Sent())
	}

	l := alertLoop(b, "skip-holder", read, desk)
	if err := l.contextPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	if err := l.contextAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	got := contextCmds(t, r, b)
	if len(got) != 1 || !strings.HasPrefix(got[0], "master||context 88%") || len(desk.Sent()) != 1 {
		t.Fatalf("sent %q, %q", got, desk.Sent())
	}
}

// During a project pause the fill is still published but nobody is alerted:
// the episode waits, and the alert goes out once the pause ends.
func TestContextAlertWaitsForThePause(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "pause-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	registerSession(t, b, "coder", "p1", "s1")
	read := func(s string) (quota.ContextReading, bool) {
		return quota.ContextReading{Pct: 90, ObservedAt: time.Now().UnixMilli(), Session: s}, s == "s1"
	}
	desk := &fakeDesktop{}
	l := alertLoop(b, "pause-holder", read, desk)
	l.rt = newFakeRT(alwaysReady)
	l.cfg.Horizon = bus.DefaultHorizon
	if _, err := b.SetPause(ctx, "pilot", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := l.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if fills, _ := b.Context(ctx); fills["coder"].Pct != 90 {
		t.Fatalf("not published during the pause: %+v", fills)
	}
	if got := contextCmds(t, r, b); len(got) != 0 || len(desk.Sent()) != 0 {
		t.Fatalf("alerted during the pause: %q, %q", got, desk.Sent())
	}
	if _, err := b.Resume(ctx, "pilot", ""); err != nil {
		t.Fatal(err)
	}
	if err := l.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := contextCmds(t, r, b); len(got) != 2 || !strings.HasPrefix(got[0], "coder||context 90%") || len(desk.Sent()) != 1 {
		t.Fatalf("after the pause: %q, %q", got, desk.Sent())
	}
}
