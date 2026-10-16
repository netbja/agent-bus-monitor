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

// F2: a coordinator hold that expires at its reset (readings now unknown)
// leaves the quota pause in place; FencedResume ends it at reset + margin.
func TestRunQuotaPauseOutlivesAnExpiredHold(t *testing.T) {
	b, _ := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	now := time.Now()
	r := quota.Reading{Provider: "anthropic", ObservedAt: now.UnixMilli(),
		Windows: []quota.Window{{Name: "5h", UsedPct: 99, ResetsAt: now.Add(1500*time.Millisecond).Unix() + 1}}}
	f := &fakeQuota{}
	f.set(r)
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "a quota pause", func() bool { p, _ := b.Pause(context.Background()); return p != nil })
	f.set(quota.Unknown("anthropic", "x", "gone"))
	time.Sleep(3500 * time.Millisecond) // past the reset, well before reset + 2 min
	if marks, _ := b.QuotaMarks(context.Background()); marks["hold:anthropic"] != "" {
		t.Fatal("the expired hold was kept")
	}
	if p, _ := b.Pause(context.Background()); p == nil {
		t.Fatal("the quota pause was deleted at the reset instead of reset + margin")
	}
}

// A quota reset granted before the announced time: the readings have no
// window any more, but the provider served a call after the hit. The hold
// and the coordinator's quota pause end at once, and master is told to go on.
func TestRunQuotaEndsWhenTheProviderServesAgain(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	now := time.Now()
	weekly := quota.Reading{Provider: "anthropic", ObservedAt: now.UnixMilli(),
		Windows: []quota.Window{{Name: "7d", UsedPct: 100, ResetsAt: now.Add(96 * time.Hour).Unix(), Minutes: 10080}}}
	f := &fakeQuota{}
	f.set(weekly)
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "a quota pause", func() bool { p, _ := b.Pause(context.Background()); return p != nil })

	// Unknown and never served since: the hold stands (the pre-v0.14.6 rule).
	f.set(quota.Unknown("anthropic", "statusline", "no reading"))
	time.Sleep(200 * time.Millisecond)
	if p, _ := b.Pause(context.Background()); p == nil {
		t.Fatal("an unknown reading ended the pause")
	}
	// Served a minute ago: over.
	served := quota.Unknown("anthropic", "statusline", "no reading")
	served.AvailableAt = time.Now().Add(-time.Minute).UnixMilli()
	f.set(served)
	waitFor(t, 3*time.Second, "the hold and the pause ended", func() bool {
		p, _ := b.Pause(context.Background())
		marks, _ := b.QuotaMarks(context.Background())
		return p == nil && marks["hold:anthropic"] == ""
	})
	msgs, _ := r.XRevRangeN(context.Background(), bus.StreamKey(b.Project(), "cmd"), "+", "-", 5).Result()
	found := false
	for _, m := range msgs {
		if m.Values["target"] == "master" && strings.Contains(m.Values["command"].(string), "Project resumed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("master got no resume directive: %+v", msgs)
	}
}

// v0.15 §3: when a hold ends, the provider's agents with accepted work or a
// declared working are told to resume, once per episode; the others are not,
// and the escalation target gets one wake=no notice naming them.
func TestRunQuotaReleaseResumesInterruptedWork(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "foureyes", "p-4", "openai")
	registerAs(t, b, "coder", "p-coder", "openai")
	registerAs(t, b, "kimi", "p-kimi", "openai")
	registerAs(t, b, "arch", "p-arch", "openai")
	if _, err := b.Status(ctx, "coder", "working", "mid-build", bus.AgentIdent{Pane: "p-coder", PID: testPID, PIDStart: testStart, Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, "arch", "blocked", "needs Bernard", bus.AgentIdent{Pane: "p-arch", PID: testPID, PIDStart: testStart, Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	board := bus.BoardKey(b.Project())
	r.HSet(ctx, board, "plan-v7-review", `{"owner":"foureyes","state":"accepted"}`, "later", `{"owner":"kimi","state":"requested"}`)
	t.Cleanup(func() { r.Del(ctx, board) })

	reset := time.Now().Add(time.Hour).Unix()
	at := func(pct float64) quota.Reading {
		return quota.Reading{Provider: "openai", ObservedAt: time.Now().UnixMilli(),
			Windows: []quota.Window{{Name: "5h", UsedPct: pct, ResetsAt: reset, Minutes: 300}}}
	}
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), at(97))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "openai held", func() bool {
		m, _ := b.QuotaMarks(ctx)
		return m["hold:openai"] != ""
	})
	resumes := func() map[string]int {
		out := map[string]int{}
		for _, m := range entries(t, r, b, "cmd") {
			cmd, _ := m.Values["command"].(string)
			if m.Values["from"] == "dispatch" && strings.Contains(cmd, "limit (openai) has reset") {
				out[m.Values["target"].(string)]++
			}
			if m.Values["from"] == "dispatch" && strings.HasPrefix(cmd, "openai's limit has reset") {
				if m.Values["wake"] != "no" || !strings.Contains(cmd, "coder, foureyes") {
					t.Errorf("notice %v", m.Values)
				}
				out["@notice"]++
			}
		}
		return out
	}
	// Release, flap back to the same reset, release again: one episode.
	for _, pct := range []float64{40, 97, 40} {
		f.set(reading("anthropic", 10), at(pct))
		time.Sleep(150 * time.Millisecond)
	}
	waitFor(t, 3*time.Second, "resume directives", func() bool { return resumes()["@notice"] == 1 })
	got := resumes()
	if got["foureyes"] != 1 || got["coder"] != 1 || got["kimi"] != 0 || got["arch"] != 0 || got["master"] != 0 {
		t.Fatalf("resumes = %v, want foureyes and coder once, nobody else", got)
	}
	for _, m := range entries(t, r, b, "cmd") {
		if m.Values["target"] == "foureyes" && m.Values["from"] == "dispatch" {
			if cmd, _ := m.Values["command"].(string); !strings.Contains(cmd, "plan-v7-review") {
				t.Fatalf("foureyes directive %q does not name its task", cmd)
			}
		}
	}
}

// v0.15 §8: master moved off the provider of its quota pause. On a free
// provider the pause ends with the resume directive; on a held one it is
// replaced, without a resume; a person's pause is never touched.
func TestRunQuotaPauseFollowsTheCoordinatorsProvider(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	f := &fakeQuota{}
	f.set(reading("anthropic", 99), reading("openai", 99))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "a quota pause for anthropic", func() bool {
		p, _ := b.Pause(ctx)
		return p != nil && p.Reason == "quota anthropic"
	})
	// master swapped to openai, held too: the pause now names openai.
	registerAs(t, b, "master", "p-master", "openai")
	waitFor(t, 3*time.Second, "the pause replaced for openai", func() bool {
		p, _ := b.Pause(ctx)
		return p != nil && p.Reason == "quota openai" && p.By == "dispatch"
	})
	resumed := func() int {
		n := 0
		for _, m := range entries(t, r, b, "cmd") {
			if cmd, _ := m.Values["command"].(string); m.Values["target"] == "master" && strings.HasPrefix(cmd, "Project resumed") {
				n++
			}
		}
		return n
	}
	if n := resumed(); n != 0 {
		t.Fatalf("a replaced pause sent %d resume directives", n)
	}
	// master on a free provider: the pause ends, master is told why.
	registerAs(t, b, "master", "p-master", "moonshot")
	f.set(reading("anthropic", 99), reading("openai", 99), reading("moonshot", 10))
	waitFor(t, 3*time.Second, "the pause ended", func() bool { p, _ := b.Pause(ctx); return p == nil })
	waitFor(t, 3*time.Second, "one resume directive", func() bool { return resumed() == 1 })

	// A person's pause, whatever its reason, stays.
	if _, err := b.SetPause(ctx, "bernard", "master", "quota anthropic", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if p, _ := b.Pause(ctx); p == nil || p.By != "bernard" {
		t.Fatalf("a person's pause was touched: %+v", p)
	}
}
