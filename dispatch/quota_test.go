package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

func registerAs(t *testing.T, b *bus.Bus, agent, pane, provider string) {
	t.Helper()
	if _, err := b.Status(context.Background(), agent, "idle", "", bus.AgentIdent{Pane: pane, PID: testPID, PIDStart: testStart, Provider: provider}); err != nil {
		t.Fatal(err)
	}
}

// fakeQuota is a switchable set of readings.
type fakeQuota struct {
	mu sync.Mutex
	rs []quota.Reading
}

func (f *fakeQuota) set(rs ...quota.Reading) { f.mu.Lock(); f.rs = rs; f.mu.Unlock() }
func (f *fakeQuota) read(context.Context) []quota.Reading {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]quota.Reading(nil), f.rs...)
}

func reading(provider string, pct float64) quota.Reading {
	now := time.Now()
	return quota.Reading{Provider: provider, ObservedAt: now.UnixMilli(),
		Windows: []quota.Window{{Name: "5h", UsedPct: pct, ResetsAt: now.Add(time.Hour).Unix(), Minutes: 300}}}
}

func quotaConfig(f *fakeQuota) Config {
	c := fastConfig()
	c.Quota = QuotaConfig{Read: f.read, Every: 10 * time.Millisecond}
	return c
}

func cleanupQuota(t *testing.T, b *bus.Bus) {
	r, _ := bus.Connect("")
	t.Cleanup(func() { r.Del(context.Background(), bus.QuotaKey(b.Project()), bus.PauseKey(b.Project())); r.Close() })
}

// A provider at its limit: its agents are neither prompted nor escalated
// about; another provider's agents are; the coordinator is warned once.
func TestRunQuotaHoldsOnlyTheExhaustedProvider(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	registerAs(t, b, "kimi", "p-kimi", "moonshot")
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 97), reading("moonshot", 20))
	send(t, b, "coder", "build it")
	send(t, b, "kimi", "check it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, quotaConfig(f))

	waitFor(t, 3*time.Second, "kimi woken and master warned", func() bool {
		panes := map[string]bool{}
		for _, p := range rt.Prompts() {
			panes[p.Pane] = true
		}
		return panes["p-kimi"] && panes["p-master"]
	})
	time.Sleep(200 * time.Millisecond)
	for _, p := range rt.Prompts() {
		if p.Pane == "p-coder" {
			t.Fatal("an agent of a held provider was prompted")
		}
	}
	marks, _ := b.QuotaMarks(context.Background())
	if marks["hold:openai"] == "" || marks["warn:openai:5h"] == "" {
		t.Fatalf("marks %v", marks)
	}
	budgets, _ := b.Budgets(context.Background())
	if budgets["openai"].SessionPct != 97 || len(budgets["openai"].Windows) != 1 {
		t.Fatalf("budget %+v", budgets["openai"])
	}
	evs, _, _ := b.Recent(context.Background(), []string{"cmd"}, 50)
	warns := 0
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == "master" && strings.HasPrefix(e.Message, "Quota openai 97%") {
			warns++
		}
	}
	if warns != 1 {
		t.Fatalf("%d warn directives, want 1", warns)
	}

	// The reading drops under the threshold: released, coder is woken.
	f.set(reading("anthropic", 10), reading("openai", 40), reading("moonshot", 20))
	waitFor(t, 3*time.Second, "coder woken after the release", func() bool {
		for _, p := range rt.Prompts() {
			if p.Pane == "p-coder" {
				return true
			}
		}
		return false
	})
	if marks, _ := b.QuotaMarks(context.Background()); marks["hold:openai"] != "" {
		t.Fatal("hold mark kept after the release")
	}
}

// The coordinator's provider at its limit pauses the project; a reading back
// under the threshold ends that pause, never a person's.
func TestRunQuotaPausesForTheCoordinatorsProvider(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	f := &fakeQuota{}
	f.set(reading("anthropic", 99))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "a quota pause", func() bool {
		p, _ := b.Pause(context.Background())
		return p != nil && p.Reason == "quota anthropic"
	})
	f.set(reading("anthropic", 30))
	waitFor(t, 3*time.Second, "the quota pause ended", func() bool {
		p, _ := b.Pause(context.Background())
		return p == nil
	})
}

// Unknown and aged readings never hold.
func TestRunQuotaUnknownNeverHolds(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "coder", "p-coder", "openai")
	old := reading("openai", 99)
	old.ObservedAt = time.Now().Add(-time.Hour).UnixMilli()
	f := &fakeQuota{}
	f.set(old)
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, quotaConfig(f))
	waitFor(t, 3*time.Second, "coder woken despite an aged 99%", func() bool { return len(rt.Prompts()) == 1 })
}
