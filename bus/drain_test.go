package bus

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func collect(evs *[]Event) func(Event) error {
	return func(e Event) error { *evs = append(*evs, e); return nil }
}

func TestDrainDeliversPendingThenUnreadInOrder(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, msg := range []string{"A", "B"} {
		id, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", msg)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// A is read but never ACKed: it sits in the PEL of a previous consumer.
	if err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "old", Streams: []string{stream, ">"}, Count: 1}).Err(); err != nil {
		t.Fatal(err)
	}
	id, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "C")
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, id)

	var got []Event
	n, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, collect(&got))
	if err != nil || n != 3 {
		t.Fatalf("DrainCmd n=%d err=%v, want 3 nil", n, err)
	}
	for i, e := range got {
		if e.ID != ids[i] {
			t.Fatalf("entry %d = %s, want %s (PEL first, then unread, in order)", i, e.ID, ids[i])
		}
	}
	if !got[0].Recovered || got[1].Recovered || got[2].Recovered {
		t.Fatalf("recovered flags = %v %v %v, want only the PEL entry", got[0].Recovered, got[1].Recovered, got[2].Recovered)
	}
	if got[0].Stale || got[0].AgeS < 0 {
		t.Fatalf("fresh entry flagged stale=%v age=%d", got[0].Stale, got[0].AgeS)
	}
	p, err := b.r.XPending(ctx, stream, "dev").Result()
	if err != nil || p.Count != 0 {
		t.Fatalf("pending after drain = %+v err=%v, want empty", p, err)
	}
	if ok, _ := b.Armed(ctx, "dev"); ok {
		t.Fatal("drain left the receiver lease behind")
	}
}

func TestDrainEmptyReturnsFast(t *testing.T) {
	b := dialTest(t)
	start := time.Now()
	n, err := b.DrainCmd(context.Background(), "dev", "host-1", DefaultHorizon, func(Event) error {
		t.Fatal("deliver called on an empty queue")
		return nil
	})
	if err != nil || n != 0 {
		t.Fatalf("DrainCmd n=%d err=%v, want 0 nil", n, err)
	}
	if d := time.Since(start); d >= 300*time.Millisecond {
		t.Fatalf("empty drain took %v, want <300ms", d)
	}
}

func TestDrainCreatesMissingGroup(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	// Sent before dev ever subscribed: the horizon-floor group must still hold it.
	id, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "early")
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	n, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, collect(&got))
	if err != nil || n != 1 || got[0].ID != id {
		t.Fatalf("DrainCmd n=%d err=%v got=%v, want the early entry", n, err, got)
	}
	names, err := b.GroupNames(ctx)
	if err != nil || len(names) != 1 || names[0] != "dev" {
		t.Fatalf("groups = %v err=%v, want [dev]", names, err)
	}
}

func TestDrainAcksNonAddressedSilently(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if _, err := b.Cmd(ctx, "sender", "other", CmdDirective, "", "not yours"); err != nil {
		t.Fatal(err)
	}
	mine, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "yours")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "sender", "other", CmdDirective, "", "not yours either"); err != nil {
		t.Fatal(err)
	}
	var got []Event
	n, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, collect(&got))
	if err != nil || n != 1 || len(got) != 1 || got[0].ID != mine {
		t.Fatalf("DrainCmd n=%d err=%v got=%v, want only %s", n, err, got, mine)
	}
	groups, err := b.r.XInfoGroups(ctx, stream).Result()
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %+v err=%v, want only dev's (other's group untouched)", groups, err)
	}
	if g := groups[0]; g.Name != "dev" || g.Pending != 0 || g.Lag != 0 {
		t.Fatalf("dev group = %+v, want pending 0 lag 0", g)
	}
}

func TestDrainFlagsStale(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatal(err)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldID := strconv.FormatInt(now.Add(-2*time.Hour).UnixMilli(), 10) + "-0"
	if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: oldID, Values: map[string]interface{}{
		"from": "sender", "target": "dev", "type": CmdDirective, "ref": "", "command": "old"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "fresh"); err != nil {
		t.Fatal(err)
	}
	var got []Event
	n, err := b.DrainCmd(ctx, "dev", "host-1", time.Hour, collect(&got))
	if err != nil || n != 2 {
		t.Fatalf("DrainCmd n=%d err=%v, want 2 (stale is delivered, never skipped)", n, err)
	}
	if !got[0].Stale || got[0].AgeS < 7190 || got[0].AgeS > 7300 {
		t.Fatalf("old entry stale=%v age=%d, want stale ~7200s", got[0].Stale, got[0].AgeS)
	}
	if got[1].Stale || got[1].AgeS > 5 {
		t.Fatalf("fresh entry stale=%v age=%d, want fresh", got[1].Stale, got[1].AgeS)
	}
}

func TestDrainCallbackErrorLeavesPending(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, msg := range []string{"one", "two"} {
		if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", msg); err != nil {
			t.Fatal(err)
		}
	}
	boom := errors.New("stdout closed")
	n, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, func(Event) error { return boom })
	if !errors.Is(err, boom) || n != 0 {
		t.Fatalf("DrainCmd n=%d err=%v, want 0 and the callback error", n, err)
	}
	p, err := b.r.XPending(ctx, StreamKey(b.project, "cmd"), "dev").Result()
	if err != nil || p.Count != 1 {
		t.Fatalf("pending = %+v err=%v, want the failed entry still pending", p, err)
	}
	// The next drain recovers it before reading the rest.
	var got []Event
	n, err = b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, collect(&got))
	if err != nil || n != 2 || !got[0].Recovered || got[0].Attempt != 2 {
		t.Fatalf("redrain n=%d err=%v got=%+v, want 2 with the first recovered (attempt 2)", n, err, got)
	}
}

func TestDrainReceiverBusy(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	prev := drainLeaseWait
	drainLeaseWait = 200 * time.Millisecond
	t.Cleanup(func() { drainLeaseWait = prev })
	if err := b.r.Set(ctx, ReceiverKey(b.project, "dev"), "someone-else", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.r.Del(context.Background(), ReceiverKey(b.project, "dev")) })
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "waiting"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, func(Event) error {
		t.Fatal("delivered without the receiver lease")
		return nil
	})
	if !errors.Is(err, ErrReceiverBusy) || n != 0 {
		t.Fatalf("DrainCmd n=%d err=%v, want ErrReceiverBusy", n, err)
	}
	if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second {
		t.Fatalf("busy after %v, want ~200ms (bounded wait)", d)
	}
	if v, _ := b.r.Get(ctx, ReceiverKey(b.project, "dev")).Result(); v != "someone-else" {
		t.Fatalf("lease = %q, drain must not touch a lease it never held", v)
	}
}

// A no-wake entry with a TTL carries expires_at = Redis TIME + ttl, and once
// past it a drain yields an expired, payload-less event — the shutdown path:
// a stop signal left unread must not stop the agent's next session.
func TestCmdNoWakeTTLExpiresBeforeDrain(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := b.CmdNoWakeTTL(ctx, "master", "dev", CmdDirective, "", "shutdown", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := b.r.XRange(ctx, stream, id, id).Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("entry: %v %v", msgs, err)
	}
	if msgs[0].Values["wake"] != "no" {
		t.Fatalf("wake = %v, want no", msgs[0].Values["wake"])
	}
	exp, _ := strconv.ParseInt(msgs[0].Values["expires_at"].(string), 10, 64)
	want := now.Add(10 * time.Minute).UnixMilli()
	if d := exp - want; d < -2000 || d > 2000 {
		t.Fatalf("expires_at = %d, want ≈ %d (server time + 10m)", exp, want)
	}

	if _, err := b.CmdNoWakeTTL(ctx, "master", "dev", CmdDirective, "", "stop now", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	var got []Event
	if _, err := b.DrainCmd(ctx, "dev", "host-1", DefaultHorizon, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("drained %d entries, want 2", len(got))
	}
	if got[0].Delivery == "expired" || got[0].Message != "shutdown" {
		t.Fatalf("live entry = %+v", got[0])
	}
	if got[1].Delivery != "expired" || got[1].Message != "" {
		t.Fatalf("expired entry = %+v, want delivery=expired and no body", got[1])
	}
}
