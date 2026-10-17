package dispatch

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// Codex review: a resume that fails to be written keeps the hold mark, so the
// next pass retries that one (and only that one) before the hold ends.
func TestRunQuotaReleaseRetriesAFailedResume(t *testing.T) {
	var fails atomic.Int32
	fails.Store(3)
	withHooks(t, hooks{resumeErr: func(agent string) error {
		if agent == "coder" && fails.Add(-1) >= 0 {
			return errors.New("injected write failure")
		}
		return nil
	}})
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	registerAs(t, b, "kimi", "p-kimi", "openai")
	for _, a := range []string{"coder", "kimi"} {
		if _, err := b.Status(ctx, a, "working", "mid-turn", bus.AgentIdent{Pane: "p-" + a, PID: testPID, PIDStart: testStart, Provider: "openai"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 97))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "openai held", func() bool {
		m, _ := b.QuotaMarks(ctx)
		return m["hold:openai"] != ""
	})
	f.set(reading("anthropic", 10), reading("openai", 40))
	waitFor(t, 3*time.Second, "the hold released after the retries", func() bool {
		m, _ := b.QuotaMarks(ctx)
		return m["hold:openai"] == "" && fails.Load() < 0
	})
	got := map[string]int{}
	for _, m := range entries(t, r, b, "cmd") {
		if cmd, _ := m.Values["command"].(string); m.Values["from"] == "dispatch" && strings.Contains(cmd, "limit (openai) has reset") {
			got[m.Values["target"].(string)]++
		}
	}
	if got["coder"] != 1 || got["kimi"] != 1 {
		t.Fatalf("resumes = %v, want coder and kimi once each", got)
	}
}

// v0.15 §5.1: a long hold asks for a decision once per episode: a directive
// to master for another provider, a notify for the person for master's own.
func TestRunQuotaLongHoldAsksOnce(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "foureyes", "p-4", "openai")
	r.HSet(ctx, bus.BoardKey(b.Project()), "plan-review", `{"owner":"foureyes","state":"accepted"}`)
	t.Cleanup(func() { r.Del(ctx, bus.BoardKey(b.Project())) })
	far := time.Now().Add(61 * time.Hour).Unix()
	week := func(p string, pct float64) quota.Reading {
		return quota.Reading{Provider: p, ObservedAt: time.Now().UnixMilli(),
			Windows: []quota.Window{{Name: "7d", UsedPct: pct, ResetsAt: far, Minutes: 10080}}}
	}
	f := &fakeQuota{}
	f.set(week("anthropic", 10), week("openai", 100))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	count := func() (directives, notes int) {
		for _, m := range entries(t, r, b, "cmd") {
			if cmd, _ := m.Values["command"].(string); m.Values["from"] == "dispatch" && strings.HasPrefix(cmd, "openai is held until") {
				if m.Values["target"] != "master" || !strings.Contains(cmd, "foureyes (plan-review)") || !strings.Contains(cmd, "61h") {
					t.Errorf("long-hold directive %v", m.Values)
				}
				directives++
			}
		}
		for _, m := range entries(t, r, b, "notify") {
			if msg, _ := m.Values["message"].(string); strings.HasPrefix(msg, "The project is paused until") {
				if !strings.Contains(msg, "swap master --profile <fallback>") {
					t.Errorf("long-pause notify %q", msg)
				}
				notes++
			}
		}
		return
	}
	waitFor(t, 3*time.Second, "one directive to master", func() bool { d, _ := count(); return d == 1 })
	f.set(week("anthropic", 100), week("openai", 100))
	waitFor(t, 3*time.Second, "one notify for master's own provider", func() bool { _, n := count(); return n == 1 })
	time.Sleep(300 * time.Millisecond)
	if d, n := count(); d != 1 || n != 1 {
		t.Fatalf("directives %d, notifies %d: want one each", d, n)
	}
}

// A short hold, or LongHold disabled, asks nothing.
func TestRunQuotaShortHoldAsksNothing(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "coder", "p-coder", "openai")
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 99)) // resets in 1 h
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "openai held", func() bool {
		m, _ := b.QuotaMarks(context.Background())
		return m["hold:openai"] != ""
	})
	time.Sleep(200 * time.Millisecond)
	for _, m := range entries(t, r, b, "cmd") {
		if cmd, _ := m.Values["command"].(string); strings.Contains(cmd, "is held until") {
			t.Fatalf("a 1 h hold asked: %q", cmd)
		}
	}
}

// Codex review: a release does not end the hold while a held agent is being
// swapped; once the intent goes without a resume (a failed swap), the next
// pass resumes it.
func TestRunQuotaReleaseWaitsForASwap(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	if _, err := b.Status(ctx, "coder", "working", "mid-turn", bus.AgentIdent{Pane: "p-coder", PID: testPID, PIDStart: testStart, Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), reading("openai", 97))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "openai held", func() bool { m, _ := b.QuotaMarks(ctx); return m["hold:openai"] != "" })
	if err := b.BeginSwap(ctx, "coder", bus.SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	f.set(reading("anthropic", 10), reading("openai", 40))
	time.Sleep(300 * time.Millisecond)
	if m, _ := b.QuotaMarks(ctx); m["hold:openai"] == "" {
		t.Fatal("the hold ended while coder was being swapped")
	}
	if err := b.EndSwap(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "coder resumed and the hold ended", func() bool {
		m, _ := b.QuotaMarks(ctx)
		n := 0
		for _, e := range entries(t, r, b, "cmd") {
			if cmd, _ := e.Values["command"].(string); e.Values["target"] == "coder" && strings.Contains(cmd, "limit (openai) has reset") {
				n++
			}
		}
		return m["hold:openai"] == "" && n == 1
	})
}

// v0.15.1 §4.2: the release of the episode a role was swapped away from
// earns master one notice; a role swapped during another episode, or for
// another provider, earns none.
func TestRunQuotaReleaseRemindsTheSwapBack(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	ctx := context.Background()
	registerAs(t, b, "master", "p-master", "anthropic")
	registerAs(t, b, "foureyes", "p-4", "anthropic")
	reset := time.Now().Add(time.Hour).Unix()
	ep := strconv.FormatInt(reset, 10)
	fb := map[string]string{
		"foureyes": `{"own_profile":"review","own_provider":"openai","profile":"opus","provider":"anthropic","since":1,"held_episode":"` + ep + `"}`,
		"coder":    `{"own_profile":"main","own_provider":"openai","profile":"opus","provider":"anthropic","since":1,"held_episode":"123"}`,
		"kimi":     `{"own_profile":"k","own_provider":"moonshot","profile":"opus","provider":"anthropic","since":1,"held_episode":"` + ep + `"}`,
	}
	for k, v := range fb {
		r.HSet(ctx, bus.FallbackKey(b.Project()), k, v)
	}
	at := func(pct float64) quota.Reading {
		return quota.Reading{Provider: "openai", ObservedAt: time.Now().UnixMilli(),
			Windows: []quota.Window{{Name: "5h", UsedPct: pct, ResetsAt: reset, Minutes: 300}}}
	}
	f := &fakeQuota{}
	f.set(reading("anthropic", 10), at(97))
	start(t, b, newFakeRT(alwaysReady), quotaConfig(f))
	waitFor(t, 3*time.Second, "openai held", func() bool { m, _ := b.QuotaMarks(ctx); return m["hold:openai"] != "" })
	f.set(reading("anthropic", 10), at(40))
	waitFor(t, 3*time.Second, "hold released", func() bool { m, _ := b.QuotaMarks(ctx); return m["hold:openai"] == "" })
	notices := map[string]int{}
	for _, m := range entries(t, r, b, "cmd") {
		if cmd, _ := m.Values["command"].(string); strings.HasPrefix(cmd, "openai is back.") {
			if m.Values["target"] != "master" || m.Values["wake"] != "no" {
				t.Errorf("notice %v", m.Values)
			}
			for role := range fb {
				if strings.Contains(cmd, " "+role+" runs") {
					notices[role]++
				}
			}
		}
	}
	if notices["foureyes"] != 1 || notices["coder"] != 0 || notices["kimi"] != 0 {
		t.Fatalf("notices = %v, want foureyes once only", notices)
	}
}
