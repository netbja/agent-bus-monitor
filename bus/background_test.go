package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

// progress-line §4.2: an agent's background work, its own record that
// expires on the Redis clock; a status never touches it.
func TestBackgroundRecord(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, BackgroundKey(b.project, "master"), BackgroundKey(b.project, "coder")) })

	if err := b.SetBackground(ctx, "master", "2 checks running", "master", 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	ttl := b.r.PTTL(ctx, BackgroundKey(b.project, "master")).Val()
	if ttl <= 19*time.Minute || ttl > 20*time.Minute {
		t.Fatalf("expiry %v, want about 20m", ttl)
	}
	now, _ := b.r.Time(ctx).Result()
	got, err := b.Backgrounds(ctx, []string{"master", "coder"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got["master"]
	if !ok || m.Text != "2 checks running" || m.By != "master" || now.Sub(m.Since).Abs() > 5*time.Second {
		t.Fatalf("read back %+v (Redis now %v)", got, now)
	}
	if _, ok := got["coder"]; ok {
		t.Fatalf("an agent with no record: %+v", got)
	}
	// a status leaves it alone
	if _, err := b.Status(ctx, "master", "idle", "turn ended", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Backgrounds(ctx, []string{"master"}); got["master"].Text != "2 checks running" {
		t.Fatalf("a status cleared it: %+v", got)
	}
	// replace, then clear
	if err := b.SetBackground(ctx, "master", "1 check running", "master", time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Backgrounds(ctx, []string{"master"}); got["master"].Text != "1 check running" {
		t.Fatalf("not replaced: %+v", got)
	}
	if err := b.ClearBackground(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Backgrounds(ctx, []string{"master"}); len(got) != 0 {
		t.Fatalf("not cleared: %+v", got)
	}
	// it expires on its own
	if err := b.SetBackground(ctx, "coder", "build", "coder", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if got, _ := b.Backgrounds(ctx, []string{"coder"}); len(got) != 0 {
		t.Fatalf("outlived its time: %+v", got)
	}
	if c, kind := ClassOf(b.project, BackgroundKey(b.project, "coder")); c != KeyRuntime || kind != "background:*" {
		t.Fatalf("class %v %q", c, kind)
	}
}

// The limits: a valid agent, one line of at most 60 characters, 0 < for <= 24h.
func TestBackgroundRefuses(t *testing.T) {
	b := &Bus{project: "demo"} // refused before any Redis call
	ctx := context.Background()
	for _, c := range []struct {
		agent, text string
		d           time.Duration
		want        string
	}{
		{"Bad Name", "x", time.Minute, "agent"},
		{"master", "", time.Minute, "empty"},
		{"master", strings.Repeat("x", 61), time.Minute, "60"},
		{"master", "two\nlines", time.Minute, "one line"},
		{"master", "x", 0, "for"},
		{"master", "x", time.Nanosecond, "1ms"}, // reviewer-codex: PEXPIRE 0 would expire it at once
		{"master", "x", 999 * time.Microsecond, "1ms"},
		{"master", "x", 25 * time.Hour, "24h"},
	} {
		if err := b.SetBackground(ctx, c.agent, c.text, "master", c.d); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q %v: %v, want %q", c.agent, c.text, c.d, err, c.want)
		}
	}
	if err := b.ClearBackground(ctx, "Bad Name"); err == nil {
		t.Error("clear took an invalid agent")
	}
}
