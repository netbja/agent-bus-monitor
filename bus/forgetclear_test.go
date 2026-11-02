package bus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// retiredPop registers a pop with a consumer group, its handled directive
// drained, and retire's own intent held; it returns that intent.
func retiredPop(t *testing.T, b *Bus, role string) string {
	t.Helper()
	ctx := context.Background()
	p := b.Project()
	if _, err := b.Status(ctx, role, "idle", "done", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "master", role, "directive", "", "handled already"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DrainCmd(ctx, role, role, DefaultHorizon, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	in := SwapIntent{By: "master", OldPID: 42, OldStart: 4200, Kind: SwapKindRetire, Nonce: NewIntentNonce()}
	if err := b.BeginSwap(ctx, role, in); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.r.Del(ctx, SwapKey(p, role), GateKey(p, role))
		b.r.HDel(ctx, AsksKey(p), "open:"+role)
		b.r.HDel(ctx, BoardKey(p), "pop-task")
		b.r.XGroupDestroy(ctx, StreamKey(p, "cmd"), role)
		b.r.HDel(ctx, AgentsKey(p), role)
	})
	return in.Encode()
}

func registered(t *testing.T, b *Bus, role string) bool {
	t.Helper()
	snaps, err := b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, ok := snaps[role]
	return ok
}

func TestForgetIfClearForgetsAndKeepsTheGroup(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	own := retiredPop(t, b, "pop-a")
	res, err := b.ForgetIfClear(ctx, "pop-a", own)
	if err != nil || !res.Forgot {
		t.Fatalf("ForgetIfClear = %+v, %v", res, err)
	}
	if registered(t, b, "pop-a") {
		t.Fatal("the registration was kept")
	}
	// The group and its cursor stay: a respawn reads on, never replaying.
	if err := b.EnsureGroup(ctx, "pop-a", DefaultHorizon); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "master", "pop-a", "directive", "", "after the retire"); err != nil {
		t.Fatal(err)
	}
	var got []string
	if _, err := b.DrainCmd(ctx, "pop-a", "pop-a", DefaultHorizon, func(e Event) error { got = append(got, e.Message); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "after the retire" {
		t.Fatalf("after a respawn the drain printed %q, want only the new directive", got)
	}
}

func TestForgetIfClearKeepsWhatWaits(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	for _, c := range []struct {
		name, why string
		arrange   func(role string)
	}{
		{"waking directive", "unread", func(role string) { b.Cmd(ctx, "master", role, "directive", "", "new work") }},
		{"pending entry", "pending", func(role string) {
			b.Cmd(ctx, "master", role, "directive", "", "delivered, not acked")
			b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: role, Consumer: role, Streams: []string{StreamKey(p, "cmd"), ">"}, Count: 10})
		}},
		{"open ask", "open ask", func(role string) { b.r.HSet(ctx, AsksKey(p), "open:"+role, "1-0") }},
		{"owned task", "board task", func(role string) {
			b.r.HSet(ctx, BoardKey(p), "pop-task", `{"owner":"`+role+`","state":"accepted"}`)
		}},
		{"gate", "gate", func(role string) { b.r.HSet(ctx, GateKey(p, role), "ref", "foureyes|x") }},
		{"intent ended", "intent", func(role string) { b.r.Del(ctx, SwapKey(p, role)) }},
		{"another intent", "intent", func(role string) {
			b.r.Set(ctx, SwapKey(p, role), `{"kind":"reload"}`, time.Minute)
		}},
	} {
		role := "pop-" + strings.ReplaceAll(c.name, " ", "-")
		own := retiredPop(t, b, role)
		c.arrange(role)
		res, err := b.ForgetIfClear(ctx, role, own)
		if err != nil || res.Forgot || !strings.Contains(strings.Join(res.Held, "; "), c.why) {
			t.Errorf("%s: %+v, %v; want kept for %q", c.name, res, err, c.why)
		}
		if !registered(t, b, role) {
			t.Errorf("%s: the registration went", c.name)
		}
	}
	// A wake=no notice and a task done hold nothing.
	own := retiredPop(t, b, "pop-quiet")
	b.CmdNoWake(ctx, "dispatch", "pop-quiet", "directive", "", "a notice")
	b.r.HSet(ctx, BoardKey(p), "pop-task", `{"owner":"pop-quiet","state":"done"}`)
	if res, err := b.ForgetIfClear(ctx, "pop-quiet", own); err != nil || !res.Forgot {
		t.Fatalf("a notice and a done task held it: %+v, %v", res, err)
	}
}

// No mission starts in the retire window (reviewer-codex): while retire's
// intent holds the role, the conductor's request and challenge to it are
// refused, and the forget still finds its own intent.
func TestRetireIntentFencesMissionStarts(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	own := retiredPop(t, b, "pop-m")
	if ok, err := b.AcquireConductor(ctx, "tokm", 15*time.Second); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	t.Cleanup(func() { b.ReleaseConductor(ctx, "tokm") })
	if _, err := b.FencedRequest(ctx, "tokm", "pop-task", "conductor", "pop-m", "", "attempt"); err == nil || !strings.Contains(err.Error(), "being swapped") {
		t.Fatalf("request during the retire = %v, want refused", err)
	}
	if err := b.FencedChallenge(ctx, "tokm", "pop-m", "ref-m", "conductor", "review"); err == nil || !strings.Contains(err.Error(), "being swapped") {
		t.Fatalf("challenge during the retire = %v, want refused", err)
	}
	if res, err := b.ForgetIfClear(ctx, "pop-m", own); err != nil || !res.Forgot {
		t.Fatalf("ForgetIfClear = %+v, %v", res, err)
	}
}
