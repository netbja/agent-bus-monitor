package bus

import (
	"context"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestTransportDoesNotRefreshWorkUpdated(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "task", "sender", "dev", "", "body", 0); err != nil {
		t.Fatal(err)
	}
	entries, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := entries["task"]
	e.Updated = 123
	if err := b.boardSet(ctx, BoardKey(b.project), "task", e); err != nil {
		t.Fatal(err)
	}
	if err := b.WatchCmdDelivery(ctx, "dev", "new", "0", func(Event) (bool, error) {
		entries, err := b.Board(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if entries["task"].Updated != 123 {
			t.Fatal("attempt refreshed work timestamp")
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, err = b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries["task"].Updated != 123 || entries["task"].Request.OutputWrittenAt == 0 {
		t.Fatal("transport/work clocks conflated")
	}
}

func TestLegacyArmCannotOverwriteReceiverFence(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Arm(ctx, "dev", "legacy", time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Disarm(ctx, "dev") })
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "body"); err != nil {
		t.Fatal(err)
	}
	if err := b.WatchCmdDelivery(ctx, "dev", "new-consumer", "0", func(Event) (bool, error) {
		before, err := b.r.Get(ctx, ReceiverKey(b.project, "dev")).Result()
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Arm(ctx, "dev", "old-overwrite", time.Minute); err != nil {
			t.Fatal(err)
		}
		m, err := b.ArmedAgents(ctx)
		if err != nil || m["dev"] != "new-consumer" {
			t.Fatalf("presence %v %v", m, err)
		}
		if err := b.Disarm(ctx, "dev"); err != nil {
			t.Fatal(err)
		}
		after, err := b.r.Get(ctx, ReceiverKey(b.project, "dev")).Result()
		if err != nil || before != after {
			t.Fatal("legacy changed fence")
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

type serverClockHook struct{ now time.Time }

func (h serverClockHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h serverClockHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h serverClockHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "time" {
			cmd.(*redis.TimeCmd).SetVal(h.now)
			return nil
		}
		return next(ctx, cmd)
	}
}

// Inject the broker clock response while leaving the local clock unchanged:
// both delivery and derived availability must follow the broker TIME response.
func TestExpiryUsesBrokerClock(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "expired"}[expired], func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			if _, err := b.Request(ctx, "task", "sender", "dev", "", "body", time.Hour); err != nil {
				t.Fatal(err)
			}
			entries, err := b.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now := time.UnixMilli(entries["task"].Request.ExpiresAt).Add(-time.Hour)
			if expired {
				now = now.Add(2 * time.Hour)
			}
			b.r.AddHook(serverClockHook{now})
			entries, err = b.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := "retained"
			if expired {
				want = "expired"
			}
			if entries["task"].Request.Availability != want {
				t.Fatalf("availability not from broker: %+v", entries)
			}
			if err := b.WatchCmdDelivery(ctx, "dev", "consumer", "0", func(e Event) (bool, error) {
				if (e.Delivery == "expired") != expired {
					t.Fatalf("delivery uses local clock: %+v", e)
				}
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
