package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/redis/go-redis/v9"
)

func dialHealth(t *testing.T) *Bus {
	t.Helper()
	b := dialTest(t)
	t.Cleanup(func() { _, _ = DeleteProject(context.Background(), b.r, b.project) })
	return b
}

func installHealthProject(t *testing.T, b *Bus, token string) {
	t.Helper()
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, token, time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatalf("MarkServicesInstalled: %v", err)
	}
	if _, err := b.Status(ctx, "sentinel", "idle", "", AgentIdent{}); err != nil {
		t.Fatalf("register sentinel: %v", err)
	}
}

func TestFencedHealthPutTransitionsAndWakesOncePerEpisode(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	put := func(status health.Status) {
		t.Helper()
		err := b.FencedHealthPut(ctx, "holder", "master", health.Record{
			ID: "herdr-probe", Status: status, Detail: "probe response checked", Fingerprint: "probe-v1",
		})
		if err != nil {
			t.Fatalf("FencedHealthPut(%s): %v", status, err)
		}
	}
	put(health.Fail)
	put(health.Fail)
	got, err := b.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if rec := got["herdr-probe"]; rec.Status != health.Fail || rec.Since == 0 || rec.ObservedAt < rec.Since {
		t.Fatalf("stored record = %+v", rec)
	}
	transitions, _, err := b.HealthTransitions(ctx, "", 10)
	if err != nil || len(transitions) != 1 || transitions[0].Status != health.Fail {
		t.Fatalf("transitions = %+v, %v", transitions, err)
	}
	cmds, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil || len(cmds) != 1 {
		t.Fatalf("fail wake count = %d, %v; want 1", len(cmds), err)
	}
	if toStringMap(cmds[0].Values)["target"] != "sentinel" {
		t.Fatalf("fail wake target = %v, want sentinel", cmds[0].Values["target"])
	}
	put(health.Warn)
	put(health.Fail)
	transitions, _, err = b.HealthTransitions(ctx, "", 10)
	cmds, _ = b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil || len(transitions) != 3 || len(cmds) != 2 {
		t.Fatalf("after second fail episode: transitions=%d cmd=%d err=%v", len(transitions), len(cmds), err)
	}
}

func TestFencedHealthPutRejectsStaleTokenWithoutWrites(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	err := b.FencedHealthPut(ctx, "stale", "master", health.Record{
		ID: "services", Status: health.Fail, Detail: "service inactive", Fingerprint: "one",
	})
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("FencedHealthPut stale token = %v", err)
	}
	got, _ := b.Health(ctx)
	if len(got) != 0 {
		t.Fatalf("stale token wrote health: %+v", got)
	}
	cmds, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	if len(cmds) != 0 {
		t.Fatalf("stale token wrote %d commands", len(cmds))
	}
}

func TestFencedHealthDailyWakeIsOncePerDay(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	first, err := b.FencedHealthDailyWake(ctx, "holder", "master")
	if err != nil || !first {
		t.Fatalf("first daily wake = %v, %v", first, err)
	}
	second, err := b.FencedHealthDailyWake(ctx, "holder", "master")
	if err != nil || second {
		t.Fatalf("second daily wake = %v, %v", second, err)
	}
	cmds, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil || len(cmds) != 1 {
		t.Fatalf("daily wake entries = %d, %v", len(cmds), err)
	}
}

func TestHealthNoteDoesNotChangeMeasuredRecord(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	if err := b.FencedHealthPut(ctx, "holder", "master", health.Record{
		ID: "services", Status: health.Warn, Detail: "service restart budget low", Fingerprint: "budget-low",
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.PutHealthNote(ctx, health.Note{ID: "services", Author: "sentinel", Text: "restart budget is low; inspect service history"}); err != nil {
		t.Fatal(err)
	}
	recs, _ := b.Health(ctx)
	notes, _ := b.HealthNotes(ctx)
	if recs["services"].Status != health.Warn || notes["services"].Text == "" || notes["services"].At == 0 {
		t.Fatalf("record=%+v note=%+v", recs["services"], notes["services"])
	}
}

func TestHealthKeysDoNotProveProjectExists(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	if err := b.r.Set(ctx, DispatchKey(b.project), "holder", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.r.HSet(ctx, HealthKey(b.project), "services", "{}").Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: HealthLogKey(b.project), Values: map[string]any{"status": "warn"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.r.HSet(ctx, HealthNotesKey(b.project), "services", "{}").Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.r.Set(ctx, HealthDailyKey(b.project), "day", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if alive, err := b.HasKeysBesidesDispatch(ctx); err != nil || alive {
		t.Fatalf("health-only keys prove project alive = %v, %v", alive, err)
	}
	if err := b.r.Del(ctx, DispatchKey(b.project)).Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{HealthKey(b.project), HealthLogKey(b.project), HealthNotesKey(b.project), HealthDailyKey(b.project)} {
		if _, ok := projectFromKey(key); ok {
			t.Errorf("health key %q names a project", key)
		}
	}
	projects, err := Projects(ctx, b.r)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.Project == b.project {
			t.Fatalf("health-only project was listed: %+v", p)
		}
	}
}

// A failed read between two failures (fail, unknown, fail) is one episode:
// sentinel is woken once, while the log still records every transition.
func TestFencedHealthPutUnknownDoesNotOpenANewFailEpisode(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	for _, st := range []health.Status{health.Fail, health.Unknown, health.Fail} {
		if err := b.FencedHealthPut(ctx, "holder", "master", health.Record{
			ID: "herdr-probe", Status: st, Detail: "probe", Fingerprint: "probe-v1"}); err != nil {
			t.Fatalf("FencedHealthPut(%s): %v", st, err)
		}
	}
	cmds, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	transitions, _, _ := b.HealthTransitions(ctx, "", 10)
	if len(cmds) != 1 || len(transitions) != 3 {
		t.Fatalf("wakes=%d transitions=%d, want 1 wake and 3 transitions", len(cmds), len(transitions))
	}
}

// Without a registered sentinel the daily round wakes nobody: a daily
// directive to master would only spend its tokens.
func TestFencedHealthDailyWakeNeedsSentinel(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	if err := b.r.HDel(ctx, AgentsKey(b.project), "sentinel").Err(); err != nil {
		t.Fatal(err)
	}
	if sent, err := b.FencedHealthDailyWake(ctx, "holder", "master"); err != nil || sent {
		t.Fatalf("daily wake without sentinel = %v, %v; want nothing sent", sent, err)
	}
	if n, _ := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Result(); n != 0 {
		t.Fatalf("cmd entries = %d, want 0", n)
	}
}

func TestLegacyPasswordInUseComparesWithoutDisclosing(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		m    map[string]string
		want bool
	}{
		{map[string]string{}, true},
		{map[string]string{"REDIS_PASSWORD": LegacyPassword}, true},
		{map[string]string{"REDIS_PASSWORD": "generated-x"}, false},
		{map[string]string{"REDIS_URL": "redis://:" + LegacyPassword + "@127.0.0.1:6380"}, true},
		{map[string]string{"REDIS_URL": "redis://:generated-x@127.0.0.1:6380", "REDIS_PASSWORD": LegacyPassword}, false},
	}
	for i, c := range cases {
		if got := LegacyPasswordInUse(env(c.m)); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}
