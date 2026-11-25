package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// quota-per-provider (the person, 2026-11-25): a default for every provider
// and an optional value per provider. A provider's own value wins over the
// default; one left at 0 is the default's. A drain no file sets follows the
// warning in force (the provider's); a drain set by the files stays, raised to
// a provider's warning above it. A provider whose resolved order is broken
// (warn <= drain <= hold) runs on the defaults in force, never half of each.
func TestQuotaThresholdsFor(t *testing.T) {
	base := QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: time.Minute}
	set := base
	set.Drain, set.DrainSet = 88, true
	cases := []struct {
		name string
		th   QuotaThresholds
		p    QuotaLevels
		want QuotaLevels
		from string
	}{
		{"no provider value", base, QuotaLevels{}, QuotaLevels{85, 85, 95}, ""},
		{"hold only", base, QuotaLevels{Hold: 90}, QuotaLevels{85, 85, 90}, "provider"},
		{"warn: the unset drain follows it", base, QuotaLevels{Warn: 70}, QuotaLevels{70, 70, 95}, "provider"},
		{"all three", base, QuotaLevels{60, 70, 80}, QuotaLevels{60, 70, 80}, "provider"},
		{"a set drain stays", set, QuotaLevels{Warn: 70}, QuotaLevels{70, 88, 95}, "provider"},
		{"a set drain raised to the warn", set, QuotaLevels{Warn: 90}, QuotaLevels{90, 90, 95}, "provider"},
		{"hold under the warn: the defaults", base, QuotaLevels{Hold: 80}, QuotaLevels{85, 85, 95}, "order"},
		{"drain over the hold: the defaults", base, QuotaLevels{Drain: 97}, QuotaLevels{85, 85, 95}, "order"},
		{"the defaults' own order broken", QuotaThresholds{Warn: 90, Drain: 80, Hold: 95}, QuotaLevels{}, QuotaLevels{85, 85, 95}, "order"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			th := c.th
			th.Providers = map[string]QuotaLevels{"openai": c.p}
			got, from := th.For("openai")
			if got != c.want || from != c.from {
				t.Fatalf("For = %+v %q, want %+v %q", got, from, c.want, c.from)
			}
			if c.th.Warn == 85 && c.th.Drain == 85 && !c.th.DrainSet { // another provider keeps the defaults, whatever openai's value
				if other, from := th.For("xai"); other != (QuotaLevels{85, 85, 95}) || from != "" {
					t.Fatalf("another provider: %+v %q", other, from)
				}
			}
		})
	}
}

// One provider held at its own threshold leaves the others running: openai's
// warning 70 and hold 80 hold coder at 85; moonshot at 85 is under the default 95 and
// kimi is woken; moonshot's own warning at 90 sends no warning at 86, which
// the default 85 would.
func TestRunQuotaPerProviderThresholds(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	registerAs(t, b, "kimi", "p-kimi", "moonshot")
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 85), reading("moonshot", 86))
	th := &fakeThresholds{}
	th.set(QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: 30 * time.Minute,
		Providers: map[string]QuotaLevels{"openai": {Warn: 70, Hold: 80}, "moonshot": {Warn: 90}}}, nil)
	cfg := quotaConfig(f)
	cfg.Quota.Thresholds = th.read
	send(t, b, "coder", "build it")
	send(t, b, "kimi", "check it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "kimi woken", func() bool {
		for _, p := range rt.Prompts() {
			if p.Pane == "p-kimi" {
				return true
			}
		}
		return false
	})
	waitFor(t, 3*time.Second, "openai held at its own 80", func() bool {
		marks, _ := b.QuotaMarks(context.Background())
		return marks["hold:openai"] != ""
	})
	time.Sleep(200 * time.Millisecond)
	for _, p := range rt.Prompts() {
		if p.Pane == "p-coder" {
			t.Fatal("coder prompted while openai is held")
		}
	}
	marks, _ := b.QuotaMarks(context.Background())
	if marks["hold:moonshot"] != "" || marks["hold:anthropic"] != "" {
		t.Fatalf("another provider held: %v", marks)
	}
	if n := countTo(t, b, "master", "Quota moonshot"); n != 0 {
		t.Fatalf("%d moonshot warnings at 86%% under its own 90%%", n)
	}
	if n := countTo(t, b, "master", "Quota openai 85%"); n != 1 {
		t.Fatalf("%d openai warnings at 85%%, want 1", n)
	}
}

// A provider's broken order is logged once, naming it, and that provider
// runs on the defaults: openai's hold at 80 under the warning 85 holds
// nothing at 85.
func TestRunQuotaProviderBrokenOrder(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 88))
	th := &fakeThresholds{}
	th.set(QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: 30 * time.Minute,
		Providers: map[string]QuotaLevels{"openai": {Warn: 90, Hold: 80}}}, nil)
	cfg := quotaConfig(f)
	cfg.Quota.Thresholds = th.read
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "openai warned at the default 85", func() bool {
		return countTo(t, b, "master", "Quota openai 88%") == 1
	})
	time.Sleep(200 * time.Millisecond)
	if marks, _ := b.QuotaMarks(context.Background()); marks["hold:openai"] != "" {
		t.Fatal("openai held by a broken order's 80")
	}
	log := cfg.Log.(*syncBuf).String()
	if n := strings.Count(log, `"action":"quota-order"`); n != 1 || !strings.Contains(log, `"provider":"openai"`) {
		t.Fatalf("the provider's broken order logged %d times:\n%s", n, log)
	}
}

// master's provider drains the run at its own drain and runs it again under
// it: anthropic's 90/95/98 does not drain at 92, over its warning, where the
// default drain 85 would; drains at 96, and runs again at 92.
func TestRunQuotaProviderDrain(t *testing.T) {
	f := &fakeThresholds{}
	f.set(QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: 30 * time.Minute,
		Providers: map[string]QuotaLevels{"anthropic": {Warn: 90, Drain: 95, Hold: 98}}}, nil)
	b, q, cfg := drainWorld(t, f)
	q.set(reading("anthropic", 92))
	start(t, b, newFakeRT(alwaysReady), cfg)
	// master's warning is sent after the drain is judged, in the same pass
	waitFor(t, 3*time.Second, "master warned at 92", func() bool { return countTo(t, b, "master", "Quota anthropic 92%") == 1 })
	if s, n := runState(b), countTo(t, b, "master", "Run draining"); s != bus.RunRunning || n != 0 {
		t.Fatalf("the run is %q (drain told %d times) at 92%% under anthropic's own drain 95", s, n)
	}
	q.set(reading("anthropic", 96))
	waitFor(t, 3*time.Second, "the run draining at anthropic's own 95", func() bool { return runState(b) == bus.RunDraining })
	q.set(reading("anthropic", 92)) // under its drain, over the default's
	waitFor(t, 3*time.Second, "the run running again under anthropic's drain", func() bool { return runState(b) == bus.RunRunning })
}

// A provider's hold above the default releases under its own value: openai
// held at 99 under its hold 98 is released at 96, which the default 95 would
// keep held.
func TestRunQuotaProviderHoldReleases(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 99))
	th := &fakeThresholds{}
	th.set(QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: 30 * time.Minute,
		Providers: map[string]QuotaLevels{"openai": {Hold: 98}}}, nil)
	cfg := quotaConfig(f)
	cfg.Quota.Thresholds = th.read
	start(t, b, newFakeRT(alwaysReady), cfg)
	held := func() bool { m, _ := b.QuotaMarks(context.Background()); return m["hold:openai"] != "" }
	waitFor(t, 3*time.Second, "openai held at 99", held)
	r := reading("openai", 96)
	r.Windows[0].ResetsAt = f.read(context.Background())[1].Windows[0].ResetsAt // the same window, lower
	f.set(reading("anthropic", 10), r)
	waitFor(t, 3*time.Second, "openai released at 96 under its own 98", func() bool { return !held() })
}
