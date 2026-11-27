package dispatch

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agent-resource-limits §5, step 3e: each agent's observation carries its
// share of its holder build's cap (the unit's cap over its terminals); a
// pane of no build read, or a build with no cap, publishes none.
func TestObservePublishesTheMemCap(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	const key = "v0.34.0.0123456789"
	register(t, b, "dev", key+"/t1")
	register(t, b, "docs", "other.0123456789/t2")
	register(t, b, "ops", "p3")
	rt := newFakeRT(func(string, int) Inspection {
		in := readyIn()
		in.Usage = &Usage{Mem: 1 << 30, TopMem: 1 << 30}
		return in
	})
	cfg := fastConfig()
	cfg.Observe = 50 * time.Millisecond
	cfg.Memory.Caps = func(context.Context) (map[string]HolderCap, error) {
		return map[string]HolderCap{key: {Max: 24 << 30, Terms: 3}, "other.0123456789": {Terms: 2}}, nil
	}
	start(t, b, rt, cfg)
	var obs map[string]bus.Observation
	waitFor(t, 5*time.Second, "all observed", func() bool {
		var err error
		obs, err = b.Observed(ctx)
		return err == nil && obs["dev"].At != 0 && obs["docs"].At != 0 && obs["ops"].At != 0
	})
	if c := obs["dev"].MemCap; c != 8<<30 {
		t.Fatalf("dev's cap %d, want 8G", c)
	}
	if c := obs["docs"].MemCap; c != 0 {
		t.Fatalf("a build with no cap published %d", c)
	}
	if c := obs["ops"].MemCap; c != 0 {
		t.Fatalf("a pane of no holder build published %d", c)
	}
}

func TestHolderCapShare(t *testing.T) {
	for _, c := range []struct {
		cap  HolderCap
		want int64
	}{{HolderCap{Max: 9, Terms: 3}, 3}, {HolderCap{Max: 9}, 0}, {HolderCap{Terms: 2}, 0}} {
		if got := c.cap.Share(); got != c.want {
			t.Errorf("%+v.Share() = %d, want %d", c.cap, got, c.want)
		}
	}
}

// memWorld is a loop holding the lease, its memory notice set at 80 %.
func memWorld(t *testing.T) (*loop, *bus.Bus) {
	t.Helper()
	b, _ := dialRun(t)
	if ok, err := b.AcquireDispatch(context.Background(), "mem-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	l := &loop{b: b, token: "mem-holder", last: map[string]string{},
		cfg: Config{EscalateTo: "master", Log: io.Discard, Memory: MemoryConfig{WarnAt: func() float64 { return 80 }}}}
	return l, b
}

func memNotices(t *testing.T, b *bus.Bus) []bus.Event {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"cmd"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []bus.Event
	for _, e := range evs {
		if strings.HasPrefix(e.Message, "memory: ") {
			out = append(out, e)
		}
	}
	return out
}

// §5: master is told once per episode when an agent crosses warn_at of its
// cap; a restart of the dispatcher repeats nothing; a fresh reading below
// ends the episode, so the next rise tells again; a new process is a new
// episode; an unknown use or cap neither tells nor ends one.
func TestMemoryNoticeOncePerEpisode(t *testing.T) {
	l, b := memWorld(t)
	ctx := context.Background()
	pass := func(o Observation) {
		t.Helper()
		if err := l.memoryAlerts(ctx, map[string]Observation{"coder": o}); err != nil {
			t.Fatal(err)
		}
	}
	const g = 1 << 30
	pass(Observation{PID: 7, MemBytes: 7 * g, MemCap: 10 * g}) // 70 %: nothing
	if n := memNotices(t, b); len(n) != 0 {
		t.Fatalf("a notice below warn_at: %+v", n)
	}
	high := Observation{PID: 7, MemBytes: 21 * g, MemCap: 24 * g, TopBytes: 15 * g, TopPID: 4242}
	pass(high)
	n := memNotices(t, b)
	if len(n) != 1 || n[0].Target != "master" || n[0].NoWake {
		t.Fatalf("notices %+v", n)
	}
	for _, want := range []string{"coder uses 21.0G of its 24.0G share", "(87 %", "largest process 15.0G (pid 4242)"} {
		if !strings.Contains(n[0].Message, want) {
			t.Fatalf("notice %q lacks %q", n[0].Message, want)
		}
	}
	pass(high)
	l = &loop{b: b, token: "mem-holder", last: map[string]string{}, cfg: l.cfg} // a dispatcher restart
	pass(high)
	if n := memNotices(t, b); len(n) != 1 {
		t.Fatalf("%d notices in one episode", len(n))
	}
	pass(Observation{PID: 7, MemBytes: 21 * g}) // cap unknown: the episode stands
	pass(Observation{PID: 7, MemCap: 24 * g})   // use unknown: the episode stands
	pass(high)
	if n := memNotices(t, b); len(n) != 1 {
		t.Fatalf("an unknown reading ended the episode: %d notices", len(n))
	}
	pass(Observation{PID: 7, MemBytes: g, MemCap: 24 * g}) // below: the episode ends
	marks, _ := b.QuotaMarks(ctx)
	if _, open := marks[bus.MemoryMark("coder")]; open {
		t.Fatalf("the episode's mark stays: %v", marks)
	}
	pass(high)
	if n := memNotices(t, b); len(n) != 2 {
		t.Fatalf("a new rise told %d times in all, want 2", len(n))
	}
	high.PID = 8 // a new process: a new episode
	pass(high)
	if n := memNotices(t, b); len(n) != 3 {
		t.Fatalf("a new process told %d times in all, want 3", len(n))
	}
}

// warn_at 0 tells nothing; the escalation target's own crossing is told
// wake=no.
func TestMemoryNoticeOffAndSelf(t *testing.T) {
	l, b := memWorld(t)
	ctx := context.Background()
	high := map[string]Observation{"master": {PID: 7, MemBytes: 9 << 30, MemCap: 10 << 30}}
	l.cfg.Memory.WarnAt = func() float64 { return 0 }
	if err := l.memoryAlerts(ctx, high); err != nil {
		t.Fatal(err)
	}
	if n := memNotices(t, b); len(n) != 0 {
		t.Fatalf("a notice with warn_at off: %+v", n)
	}
	l.cfg.Memory.WarnAt = func() float64 { return 80 }
	if err := l.memoryAlerts(ctx, high); err != nil {
		t.Fatal(err)
	}
	if n := memNotices(t, b); len(n) != 1 || !n[0].NoWake {
		t.Fatalf("the target's own notice %+v", n)
	}
}
