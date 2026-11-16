package dispatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// switchable thresholds, as the service reads them from its files each
// minute; err makes the next reads fail.
type fakeThresholds struct {
	mu    sync.Mutex
	th    QuotaThresholds
	err   error
	reads atomic.Int32
}

func (f *fakeThresholds) set(th QuotaThresholds, err error) {
	f.mu.Lock()
	f.th, f.err = th, err
	f.mu.Unlock()
}
func (f *fakeThresholds) read() (QuotaThresholds, error) {
	f.reads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.th, f.err
}

// drainWorld: master and coder on anthropic, coder holding work, a run to
// drain, the thresholds from f.
func drainWorld(t *testing.T, f *fakeThresholds) (*bus.Bus, *fakeQuota, Config) {
	t.Helper()
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "anthropic")
	if _, err := b.Request(ctx, "wiki-3", "master", "coder", "", "build it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "wiki-3", "coder"); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "boot", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if _, _, err := b.FencedRunPass(ctx, "boot", nil, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.ReleaseDispatch(ctx, "boot"); err != nil {
		t.Fatal(err)
	}
	q := &fakeQuota{}
	cfg := quotaConfig(q)
	cfg.Quota.Thresholds = f.read
	return b, q, cfg
}

func runState(b *bus.Bus) string {
	r, _ := b.Run(context.Background())
	if r == nil {
		return ""
	}
	return r.State
}

// Quota settings step 2 (§2.1, the person's Q1-Q3): master is warned and
// agents with work are told to finish their step at quota_warn; the run
// drains at quota_drain; a drained run runs again under quota_drain, not
// only under quota_warn.
func TestRunQuotaWarnBelowTheDrain(t *testing.T) {
	f := &fakeThresholds{}
	f.set(QuotaThresholds{Warn: 80, Drain: 90, Hold: 95, MaxAge: 30 * time.Minute}, nil)
	b, q, cfg := drainWorld(t, f)
	q.set(reading("anthropic", 86))
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "master and coder warned at 86%", func() bool {
		return countTo(t, b, "master", "Quota anthropic 86%") == 1 && countTo(t, b, "coder", "Quota anthropic 86%") == 1
	})
	time.Sleep(200 * time.Millisecond)
	if s := runState(b); s != bus.RunRunning || countTo(t, b, "master", "Run draining") != 0 {
		t.Fatalf("the run is %q (drain told %d times) at 86%% under a 90%% drain", s, countTo(t, b, "master", "Run draining"))
	}
	q.set(reading("anthropic", 91))
	waitFor(t, 3*time.Second, "the run draining at 91%", func() bool { return runState(b) == bus.RunDraining })
	q.set(reading("anthropic", 85)) // over the warning, under the drain
	waitFor(t, 3*time.Second, "the run running again under the drain", func() bool { return runState(b) == bus.RunRunning })
}

// warn <= drain <= hold is a rule (Q3): a broken order read from the files
// uses the defaults for all three (85, 85, 95), logged once per episode,
// never the last good set (foureyes on r2).
func TestRunQuotaBrokenOrderUsesTheDefaults(t *testing.T) {
	f := &fakeThresholds{}
	f.set(QuotaThresholds{Warn: 90, Drain: 80, Hold: 95, MaxAge: 30 * time.Minute}, nil)
	b, q, cfg := drainWorld(t, f)
	q.set(reading("anthropic", 86)) // over the default 85: warned and drained
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "the defaults applied", func() bool {
		return countTo(t, b, "master", "Quota anthropic 86%") == 1 && runState(b) == bus.RunDraining
	})
	time.Sleep(200 * time.Millisecond)
	if n := strings.Count(cfg.Log.(*syncBuf).String(), `"action":"quota-order"`); n != 1 {
		t.Fatalf("the broken order logged %d times, want once:\n%s", n, cfg.Log.(*syncBuf).String())
	}
}

// A threshold file that cannot be read keeps the last values in force.
func TestRunQuotaUnreadableThresholdsKeepTheLast(t *testing.T) {
	f := &fakeThresholds{}
	f.set(QuotaThresholds{Warn: 80, Drain: 90, Hold: 95, MaxAge: 30 * time.Minute}, nil)
	b, q, cfg := drainWorld(t, f)
	q.set(reading("anthropic", 50))
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "a first read", func() bool { return f.reads.Load() > 0 })
	f.set(QuotaThresholds{}, errors.New("config.toml does not parse"))
	q.set(reading("anthropic", 86)) // drains at the default 85, not at the last 90
	waitFor(t, 3*time.Second, "warned", func() bool { return countTo(t, b, "master", "Quota anthropic 86%") == 1 })
	time.Sleep(200 * time.Millisecond)
	if s := runState(b); s != bus.RunRunning {
		t.Fatalf("drained at 86%% with the last drain of 90%%: %q", s)
	}
}

// The thresholds are read with the readings, every q.Every, not at every
// reconcile tick (§2.2).
func TestRunQuotaThresholdsReadWithTheReadings(t *testing.T) {
	f := &fakeThresholds{}
	f.set(QuotaThresholds{Warn: 85, Drain: 85, Hold: 95, MaxAge: 30 * time.Minute}, nil)
	b, q, cfg := drainWorld(t, f)
	q.set(reading("anthropic", 10))
	cfg.Quota.Every = 400 * time.Millisecond // 20 ms ticks: about 20 per refresh
	start(t, b, newFakeRT(alwaysReady), cfg)
	time.Sleep(time.Second)
	if n := f.reads.Load(); n < 1 || n > 4 {
		t.Fatalf("thresholds read %d times in 1 s with readings every 400 ms", n)
	}
}

func countTo(t *testing.T, b *bus.Bus, target, prefix string) int {
	t.Helper()
	evs, _, _ := b.Recent(context.Background(), []string{"cmd"}, 200)
	n := 0
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == target && strings.HasPrefix(e.Message, prefix) {
			n++
		}
	}
	return n
}
