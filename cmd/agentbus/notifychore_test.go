package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/notify"
)

// fakeSender records what each target was sent; fail makes a send fail.
type fakeSender struct {
	mu   sync.Mutex
	sent map[string][]notify.Message
	fail map[string]bool
	hook func(target string) // after a successful send
}

func (f *fakeSender) senderFor(t notify.Target) notify.Sender { return &fakeTarget{f: f, t: t} }

func (f *fakeSender) count(target string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent[target])
}

type fakeTarget struct {
	f *fakeSender
	t notify.Target
}

func (s *fakeTarget) Name() string { return s.t.Name }
func (s *fakeTarget) Kind() string { return s.t.Kind }
func (s *fakeTarget) Send(_ context.Context, m notify.Message) error {
	s.f.mu.Lock()
	if s.f.fail[s.t.Name] {
		s.f.mu.Unlock()
		return &notify.SendError{Target: s.t.Name, Status: 503}
	}
	if s.f.sent == nil {
		s.f.sent = map[string][]notify.Message{}
	}
	s.f.sent[s.t.Name] = append(s.f.sent[s.t.Name], m)
	hook := s.f.hook
	s.f.mu.Unlock()
	if hook != nil {
		hook(s.t.Name)
	}
	return nil
}

// notifyHome is a config folder with notify.toml (two ntfy targets and a
// desktop) and config.toml, and a state folder.
type notifyHome struct{ home, state string }

func newNotifyHome(t *testing.T, configToml string) notifyHome {
	t.Helper()
	h := notifyHome{home: t.TempDir()}
	cfg := filepath.Join(h.home, ".config", "agentbus")
	os.MkdirAll(cfg, 0o700)
	os.WriteFile(filepath.Join(cfg, "notify.toml"), []byte(`[ntfy.phone]
topic = "agentbus-0123456789"
[ntfy.watch]
topic = "agentbus-9876543210"
[desktop.here]
`), 0o600)
	os.WriteFile(filepath.Join(cfg, "config.toml"), []byte(configToml), 0o600)
	h.state = filepath.Join(h.home, ".local", "state", "agentbus")
	return h
}

func (h notifyHome) getenv(k string) string {
	return map[string]string{"HOME": h.home}[k]
}

func notifyBus(t *testing.T) *bus.Bus {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "notify"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	if ok, err := b.AcquireDispatch(context.Background(), "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	return b
}

func newNotifier(b *bus.Bus, h notifyHome, f *fakeSender) *notifier {
	return &notifier{b: b, reader: &notifyReader{b: b}, getenv: h.getenv, now: time.Now, stateDir: h.state, sender: f.senderFor}
}

func wait(t *testing.T, b *bus.Bus, agent, msg string) {
	t.Helper()
	if _, err := b.Status(context.Background(), agent, "waiting", msg, bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // a new declaration is a new ts
}

const twoTargets = "[notify]\non = true\ntargets = [\"phone\", \"watch\"]\ndelay = \"0\"\n"

func TestNotifierBaselineThenOnceThenRestart(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, twoTargets)
	f := &fakeSender{}
	ctx := context.Background()
	wait(t, b, "coder", "standing")
	n := newNotifier(b, h, f)
	if err := n.pass(ctx, "tok"); err != nil {
		t.Fatal(err)
	}
	if f.count("phone")+f.count("watch") != 0 {
		t.Fatal("the first pass sent what already stood")
	}
	rec, _ := b.Notified(ctx)
	if rec["_target|phone"] == "" || rec["_target|watch"] == "" {
		t.Fatalf("no baseline: %v", rec)
	}
	wait(t, b, "foureyes", "new")
	for i := 0; i < 2; i++ {
		if err := n.pass(ctx, "tok"); err != nil {
			t.Fatal(err)
		}
	}
	if f.count("phone") != 1 || f.count("watch") != 1 {
		t.Fatalf("sent %d/%d, want once each", f.count("phone"), f.count("watch"))
	}
	if err := newNotifier(b, h, f).pass(ctx, "tok"); err != nil { // a restart
		t.Fatal(err)
	}
	if f.count("phone") != 1 {
		t.Fatal("a restart sent again")
	}
	l, _ := notify.ReadLast(notify.LastPath(h.state))
	if o, ok := l.Latest("phone"); !ok || !o.OK || o.Project != b.Project() || l.SentInHour("phone", time.Now()) != 1 {
		t.Fatalf("outcome %+v %v", o, ok)
	}
}

func TestNotifierWithoutTheLeaseSendsNothing(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, twoTargets)
	f := &fakeSender{}
	ctx := context.Background()
	n := newNotifier(b, h, f)
	n.pass(ctx, "tok")
	wait(t, b, "coder", "new")
	if err := n.pass(ctx, "not-the-holder"); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("err %v", err)
	}
	if f.count("phone") != 0 {
		t.Fatal("sent without the lease")
	}
}

func TestNotifierRetriesOnlyTheFailedTarget(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, twoTargets)
	f := &fakeSender{fail: map[string]bool{"watch": true}}
	ctx := context.Background()
	n := newNotifier(b, h, f)
	n.pass(ctx, "tok")
	wait(t, b, "coder", "new")
	n.pass(ctx, "tok")
	if f.count("phone") != 1 || f.count("watch") != 0 {
		t.Fatalf("%d %d", f.count("phone"), f.count("watch"))
	}
	l, _ := notify.ReadLast(notify.LastPath(h.state))
	if l.SentInHour("watch", time.Now()) != 0 {
		t.Fatal("a failed send uses up the hour")
	}
	f.fail = nil
	n.pass(ctx, "tok")
	if f.count("phone") != 1 || f.count("watch") != 1 {
		t.Fatalf("after recovery %d %d", f.count("phone"), f.count("watch"))
	}
}

// A lease lost between a target's success and its record: that target gets
// the lines once more at the next pass, never more (the accepted repeat).
func TestNotifierRepeatsOnceAfterALostRecord(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, "[notify]\non = true\ntargets = [\"phone\"]\ndelay = \"0\"\n")
	f := &fakeSender{}
	ctx := context.Background()
	n := newNotifier(b, h, f)
	n.pass(ctx, "tok")
	wait(t, b, "coder", "new")
	f.hook = func(string) { b.ReleaseDispatch(ctx, "tok") }
	n.pass(ctx, "tok")
	f.hook = nil
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("lease back")
	}
	n.pass(ctx, "tok")
	n.pass(ctx, "tok")
	if f.count("phone") != 2 {
		t.Fatalf("sent %d times, want 2 (the one repeat)", f.count("phone"))
	}
}

// A pass with a failed read takes no baseline and sends nothing; the source
// is recorded for health; the next good pass baselines what stands.
func TestNotifierAFailedReadSendsNothing(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, twoTargets)
	f := &fakeSender{}
	ctx := context.Background()
	wait(t, b, "coder", "standing")
	n := newNotifier(b, h, f)
	n.failRead = map[string]string{"asks": "boom"}
	if err := n.pass(ctx, "tok"); err == nil {
		t.Fatal("a failed read is said")
	}
	if rec, _ := b.Notified(ctx); len(rec) != 0 {
		t.Fatalf("baseline on a failed read: %v", rec)
	}
	l, _ := notify.ReadLast(notify.LastPath(h.state))
	if got := l.FailingSources(b.Project()); len(got) != 1 || got[0] != "asks" {
		t.Fatalf("sources %v", got)
	}
	n.failRead = nil
	n.pass(ctx, "tok")
	n.pass(ctx, "tok")
	if f.count("phone") != 0 {
		t.Fatal("the standing declaration was sent after the read came back")
	}
	l, _ = notify.ReadLast(notify.LastPath(h.state))
	if len(l.FailingSources(b.Project())) != 0 {
		t.Fatal("a source back still recorded")
	}
}

func TestNotifierOffClearsTheRecord(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, twoTargets)
	ctx := context.Background()
	n := newNotifier(b, h, &fakeSender{})
	n.pass(ctx, "tok")
	if rec, _ := b.Notified(ctx); len(rec) == 0 {
		t.Fatal("no baseline")
	}
	// Off with the targets still listed (foureyes: else pruning alone would
	// empty the record): nothing sent, the record cleared.
	os.WriteFile(filepath.Join(h.home, ".config", "agentbus", "config.toml"),
		[]byte("[notify]\non = false\ntargets = [\"phone\", \"watch\"]\ndelay = \"0\"\n"), 0o600)
	f := &fakeSender{}
	n.sender = f.senderFor
	wait(t, b, "coder", "new while off")
	n.pass(ctx, "tok")
	if rec, _ := b.Notified(ctx); len(rec) != 0 || f.count("phone") != 0 {
		t.Fatalf("off kept %v, sent %d", rec, f.count("phone"))
	}
}

// Two services (two projects) share one target's hour through
// notify-last.json under the host lock.
func TestNotifierTwoProjectsShareTheHour(t *testing.T) {
	h := newNotifyHome(t, "[notify]\non = true\ntargets = [\"phone\"]\ndelay = \"0\"\nmax_per_hour = 1\n")
	f := &fakeSender{}
	ctx := context.Background()
	var ns []*notifier
	for i := 0; i < 2; i++ {
		b := notifyBus(t)
		n := newNotifier(b, h, f)
		n.pass(ctx, "tok")
		wait(t, b, "coder", "new")
		ns = append(ns, n)
	}
	for _, n := range ns {
		n.pass(ctx, "tok")
	}
	if f.count("phone") != 1 {
		t.Fatalf("sent %d, want 1: the hour is the host's", f.count("phone"))
	}
}

func TestDesktopPromptTargets(t *testing.T) {
	for cfg, want := range map[string]bool{
		"[notify]\non = true\ntargets = [\"here\"]\n":                                      true,
		"[notify]\non = false\ntargets = [\"here\"]\n":                                     false,
		"[notify]\non = true\ntargets = [\"phone\"]\n":                                     false,
		"[notify]\non = true\ntargets = [\"here\"]\nevents = [\"decision\"]\n":             false,
		"[notify]\non = true\ntargets = [\"here\"]\nevents = [\"prompt\", \"decision\"]\n": true,
	} {
		h := newNotifyHome(t, cfg)
		if got := len(desktopPromptTargets(h.getenv)) > 0; got != want {
			t.Errorf("%q: %v", cfg, got)
		}
	}
}

// reviewer-codex: full detail reaches a private target scrubbed end to end,
// a bracketed path included.
func TestNotifierFullDetailIsScrubbed(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, "[notify]\non = true\ntargets = [\"here\"]\ndelay = \"0\"\ndetail = \"full\"\n")
	f := &fakeSender{}
	ctx := context.Background()
	n := newNotifier(b, h, f)
	n.pass(ctx, "tok")
	wait(t, b, "coder", "see (/home/alice/secrets) and https://x.example/a")
	n.pass(ctx, "tok")
	if f.count("here") != 1 {
		t.Fatalf("sent %d", f.count("here"))
	}
	line := f.sent["here"][0].Lines[0]
	if line != "coder waits for you (busmon For you): see ([path]) and [url]" {
		t.Fatalf("line %q", line)
	}
}

// reviewer-codex, master: the handover of prompts from the dispatcher's own
// ring to the desktop target loses none. An episode that began before the
// target's baseline is the old ring's (it was baselined, never sent); one
// that began after is the notifier's; without a baseline the ring rings.
func TestPromptSkipOnlyAfterTheDesktopBaseline(t *testing.T) {
	b := notifyBus(t)
	h := newNotifyHome(t, "[notify]\non = true\ntargets = [\"here\"]\n")
	ctx := context.Background()
	skip := promptSkip(b, h.getenv)
	if skip(ctx, time.Now().UnixMilli()) {
		t.Fatal("skipped before any baseline")
	}
	newNotifier(b, h, &fakeSender{}).pass(ctx, "tok")
	rec, _ := b.Notified(ctx)
	at, _ := strconv.ParseInt(rec["_target|here"], 10, 64)
	if skip(ctx, at-1) {
		t.Fatal("an episode older than the baseline is skipped: nobody would ring it")
	}
	if !skip(ctx, at+1) {
		t.Fatal("an episode after the baseline rings twice")
	}
	other := newNotifyHome(t, "[notify]\non = true\ntargets = [\"phone\"]\n")
	if promptSkip(b, other.getenv)(ctx, at+1) {
		t.Fatal("skipped with no desktop target listed")
	}
}
