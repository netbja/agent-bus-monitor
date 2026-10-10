package bus

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const testHorizon = 24 * time.Hour

func TestEnsureGroupCapturesEarlierCommand(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Cmd(ctx, "me", "dev", CmdDirective, "", "early"); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureGroup(ctx, "dev", testHorizon); err != nil {
		t.Fatal(err)
	}
	o, err := b.Obligation(ctx, "dev", testHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if !o.GroupExists || o.Unread != 1 || !o.Wakes() || o.Size() != 1 {
		t.Fatalf("obligation = %+v, want the earlier command unread", o)
	}
}

func TestEnsureGroupIdempotent(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.EnsureGroup(ctx, "dev", testHorizon); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureGroup(ctx, "dev", testHorizon); err != nil {
		t.Fatalf("second EnsureGroup: %v", err)
	}
	if err := b.EnsureGroup(ctx, "Bad Name", testHorizon); err == nil {
		t.Fatal("accepted invalid agent")
	}
}

func TestObligationIgnoresOtherTargetsAndNoWake(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	_ = b.EnsureGroup(ctx, "dev", testHorizon)
	b.Cmd(ctx, "me", "other", CmdDirective, "", "x")
	b.CmdNoWake(ctx, "me", "dev", CmdDirective, "", "quiet")
	id, _ := b.Cmd(ctx, "me", "dev", CmdDirective, "", "real")
	o, err := b.Obligation(ctx, "dev", testHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if o.Unread != 1 || o.OldestID != id || o.HighID != id {
		t.Fatalf("obligation = %+v, want only %s", o, id)
	}
}

// deliverOne reads the next entry for agent into its PEL without acking.
func deliverOne(t *testing.T, b *Bus, agent string) {
	t.Helper()
	res, err := b.r.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group: agent, Consumer: "c", Streams: []string{StreamKey(b.project, "cmd"), ">"}, Count: 1, Block: -1,
	}).Result()
	if err != nil || len(res) == 0 {
		t.Fatalf("XREADGROUP: %v %v", res, err)
	}
}

func TestObligationPendingFiltered(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(b *Bus){
		"other target": func(b *Bus) { b.Cmd(ctx, "me", "other", CmdDirective, "", "x") },
		"wake=no":      func(b *Bus) { b.CmdNoWake(ctx, "me", "dev", CmdDirective, "", "x") },
		"stale": func(b *Bus) {
			old := time.Now().Add(-25 * time.Hour).UnixMilli()
			b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), ID: fmt.Sprintf("%d-0", old),
				Values: map[string]interface{}{"from": "me", "target": "dev", "type": "directive", "command": "x"}})
		},
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			b := dialTest(t)
			// Group at 0 so the stale entry is delivered into the PEL.
			if err := b.r.XGroupCreateMkStream(ctx, StreamKey(b.project, "cmd"), "dev", "0").Err(); err != nil {
				t.Fatal(err)
			}
			add(b)
			deliverOne(t, b, "dev")
			o, err := b.Obligation(ctx, "dev", testHorizon)
			if err != nil {
				t.Fatal(err)
			}
			if o.Pending != 0 || o.PendingTotal != 1 || o.Wakes() {
				t.Fatalf("obligation = %+v, want Pending 0 / PendingTotal 1", o)
			}
		})
	}
}

func TestObligationPendingMissingBodyCounts(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	_ = b.EnsureGroup(ctx, "dev", testHorizon)
	b.Cmd(ctx, "me", "dev", CmdDirective, "", "x")
	deliverOne(t, b, "dev")
	b.r.XTrimMaxLen(ctx, StreamKey(b.project, "cmd"), 0)
	o, err := b.Obligation(ctx, "dev", testHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if o.Pending != 1 || o.PendingTotal != 1 || !o.Wakes() {
		t.Fatalf("obligation = %+v, want Pending 1", o)
	}
}

func TestObligationNoGroupScansStream(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.Cmd(ctx, "me", "other", CmdDirective, "", "x")
	b.CmdNoWake(ctx, "me", "dev", CmdDirective, "", "q")
	b.Cmd(ctx, "me", "dev", CmdDirective, "", "real")
	o, err := b.Obligation(ctx, "dev", testHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if o.GroupExists || o.Unread != 1 || o.GapPossible {
		t.Fatalf("obligation = %+v", o)
	}
	// No stream at all.
	b2 := dialTest(t)
	o2, err := b2.Obligation(ctx, "dev", testHorizon)
	if err != nil || o2.Wakes() || o2.GroupExists {
		t.Fatalf("empty: %+v %v", o2, err)
	}
}

func TestObligationStaleNotWaking(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), ID: fmt.Sprintf("%d-0", old),
		Values: map[string]interface{}{"from": "me", "target": "dev", "type": "directive", "command": "x"}}).Err(); err != nil {
		t.Fatal(err)
	}
	o, err := b.Obligation(ctx, "dev", testHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if o.Wakes() || o.Stale != 1 || o.OldestID != "" || o.HighID != "" {
		t.Fatalf("obligation = %+v, want stale only", o)
	}
}

func TestGapPossible(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b.Cmd(ctx, "me", "dev", CmdDirective, "", "x")
	}
	o, _ := b.Obligation(ctx, "dev", testHorizon)
	if o.GapPossible || o.Unread != 5 {
		t.Fatalf("untrimmed: %+v", o)
	}
	if err := b.r.XTrimMaxLen(ctx, stream, 2).Err(); err != nil {
		t.Fatal(err)
	}
	o, _ = b.Obligation(ctx, "dev", testHorizon)
	if !o.GapPossible || o.Unread != 2 {
		t.Fatalf("trimmed: %+v", o)
	}
}

func TestStatusCreatesGroup(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "dev", "idle", "hi", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	names, err := b.GroupNames(ctx)
	if err != nil || !reflect.DeepEqual(names, []string{"dev"}) {
		t.Fatalf("groups = %v, %v", names, err)
	}
}

func TestArmedAndGroupNamesAndTargets(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if names, err := b.GroupNames(ctx); err != nil || names != nil {
		t.Fatalf("no stream: %v %v", names, err)
	}
	if ok, err := b.Armed(ctx, "dev"); err != nil || ok {
		t.Fatalf("Armed before = %v %v", ok, err)
	}
	b.r.Set(ctx, ReceiverKey(b.project, "dev"), "tok", time.Minute)
	t.Cleanup(func() { b.r.Del(ctx, ReceiverKey(b.project, "dev")) })
	if ok, err := b.Armed(ctx, "dev"); err != nil || !ok {
		t.Fatalf("Armed after = %v %v", ok, err)
	}
	b.Cmd(ctx, "me", "dev", CmdDirective, "", "a")
	b.Cmd(ctx, "me", "dev", CmdDirective, "", "b")
	b.Cmd(ctx, "me", "alpha", CmdDirective, "", "c")
	b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), Values: map[string]interface{}{"target": "Bad Name"}})
	got, err := b.CmdTargets(ctx)
	if err != nil || !reflect.DeepEqual(got, []string{"alpha", "dev"}) {
		t.Fatalf("targets = %v, %v", got, err)
	}
	b.EnsureGroup(ctx, "dev", testHorizon)
	if names, _ := b.GroupNames(ctx); !reflect.DeepEqual(names, []string{"dev"}) {
		t.Fatalf("groups = %v", names)
	}
}
