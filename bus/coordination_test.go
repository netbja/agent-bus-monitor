package bus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
	"time"
)

// Regression: COUNT 16 put the second entry in the PEL, but rearm only read >.
func TestCoordinationOneShotDoesNotStrandBatch(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	ids := make([]string, 2)
	for i := range ids {
		var err error
		ids[i], err = b.Cmd(ctx, "sender", "dev", CmdDirective, "", "hello")
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range ids {
		c, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		var got string
		err := b.WatchCmd(c, "dev", "receiver", "0", func(e Event) bool { got = e.ID; return true })
		cancel()
		if err != nil || got != want {
			t.Fatalf("rearm %d got %s err %v, want %s", i, got, err, want)
		}
	}
}

// Regression: technical ACK must follow, not precede, the delivery callback.
func TestCoordinationAckAfterCallback(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "hello"); err != nil {
		t.Fatal(err)
	}
	err := b.WatchCmd(ctx, "dev", "receiver", "0", func(e Event) bool {
		p, err := b.r.XPending(ctx, StreamKey(b.project, "cmd"), "dev").Result()
		if err != nil || p.Count != 1 {
			t.Errorf("during callback pending=%+v err=%v; ACK happened before delivery", p, err)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCoordinationInterruptedDelivery(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(afterWrite), func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			id, err := b.Request(ctx, "task", "sender", "dev", "", "important", 0)
			if err != nil {
				t.Fatal(err)
			}
			var written bytes.Buffer
			fail := errors.New("interrupted")
			err = b.WatchCmdDelivery(ctx, "dev", "old-process", "0", func(e Event) (bool, error) {
				if e.ID != id {
					t.Fatal(e.ID)
				}
				if afterWrite {
					_, _ = written.WriteString(e.Message) // output existed before the simulated crash
				}
				return true, fail
			})
			if afterWrite && written.String() != "important" {
				t.Fatal("write boundary not reached")
			}
			if !errors.Is(err, fail) {
				t.Fatal(err)
			}
			board, err := b.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if board["task"].Request.Delivery != "uncertain" || board["task"].Request.AcceptedAt != 0 {
				t.Fatalf("unexpected metadata: %+v", board)
			}
			err = b.WatchCmdDelivery(ctx, "dev", "new-process", "0", func(e Event) (bool, error) {
				if !e.Recovered || e.Attempt != 2 || e.ID != id || e.Message != "important" {
					t.Fatalf("not recoverable: %+v", e)
				}
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			board, _ = b.Board(ctx)
			r := board["task"].Request
			if r.Delivery != "output_written" || r.Attempts != 2 || !r.DuplicatePossible || r.OutputWrittenAt == 0 || r.AcceptedAt != 0 || board["task"].State != "requested" {
				t.Fatalf("transport conflated with acceptance: %+v", r)
			}
			pending, err := b.r.XPending(ctx, StreamKey(b.project, "cmd"), "dev").Result()
			if err != nil || pending.Count != 0 {
				t.Fatalf("pending=%+v err=%v", pending, err)
			}
		})
	}
}

func TestCoordinationOldPendingBatchAndTwoTargets(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	var ids []string
	for _, target := range []string{"dev", "peer", "dev", "peer"} {
		id, err := b.Cmd(ctx, "sender", target, CmdDirective, "", "for "+target)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Simulate a killed old binary that read the whole batch and never ACKed.
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "dead", Streams: []string{stream, ">"}, Count: 16}).Err(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"dev", "peer"} {
		var want []string
		for i, id := range ids {
			if (target == "dev" && i%2 == 0) || (target == "peer" && i%2 == 1) {
				want = append(want, id)
			}
		}
		for _, id := range want {
			c, cancel := context.WithTimeout(ctx, time.Second)
			err := b.WatchCmdDelivery(c, target, "restarted", "0", func(e Event) (bool, error) {
				if e.ID != id || e.Target != target {
					t.Fatalf("routing: %+v want %s/%s", e, id, target)
				}
				if target == "dev" && !e.Recovered {
					t.Fatal("pending not identified")
				}
				return true, nil
			})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCoordinationExpiredAndTrimmed(t *testing.T) {
	for _, mode := range []string{"expired", "missing"} {
		t.Run(mode, func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			stream := StreamKey(b.project, "cmd")
			id, err := b.Request(ctx, "task", "sender", "dev", "", "do not execute", time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
					t.Fatal(err)
				}
				if err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "dead", Streams: []string{stream, ">"}, Count: 1}).Err(); err != nil {
					t.Fatal(err)
				}
				if err := b.r.XTrimMaxLen(ctx, stream, 0).Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				time.Sleep(3 * time.Millisecond)
			}
			board, err := b.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if board["task"].Request.Availability != mode {
				t.Fatal(board["task"].Request)
			}
			if err := b.AcceptRequest(ctx, "task", "dev"); err == nil {
				t.Fatal("accepted unavailable request")
			}
			failure := errors.New("output failed")
			err = b.WatchCmdDelivery(ctx, "dev", "first", "0", func(e Event) (bool, error) {
				if e.Delivery != mode || e.Message != "" || e.ID != id {
					t.Fatalf("unsafe event %+v", e)
				}
				return true, failure
			})
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			// Even a missing-body diagnostic must remain pending if its output failed.
			c, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			err = b.WatchCmdDelivery(c, "dev", "retry", "0", func(e Event) (bool, error) {
				if e.Delivery != mode || !e.Recovered {
					t.Fatal(e)
				}
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			board, _ = b.Board(ctx)
			if board["task"].Request.Delivery != mode || board["task"].State != "requested" {
				t.Fatal(board)
			}
		})
	}
}

func TestCoordinationLeaseFencesOldSubscriber(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "body"); err != nil {
		t.Fatal(err)
	}
	err := b.WatchCmdDelivery(ctx, "dev", "first", "0", func(e Event) (bool, error) {
		waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		err := b.WatchCmdDelivery(waitCtx, "dev", "second", "0", func(Event) (bool, error) { t.Fatal("concurrent delivery"); return true, nil })
		if err == nil {
			t.Fatal("second subscriber allowed")
		}
		// Simulate lease expiry followed by a new owner, during the output boundary.
		if err := b.r.Set(ctx, ReceiverKey(b.project, "dev"), "new-owner", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		return true, nil
	})
	if err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("old subscriber ACKed: %v", err)
	}
	lease, err := b.r.Get(ctx, ReceiverKey(b.project, "dev")).Result()
	if err != nil || lease != "new-owner" {
		t.Fatalf("old cleanup removed new lease %q %v", lease, err)
	}
	pending, err := b.r.XPending(ctx, StreamKey(b.project, "cmd"), "dev").Result()
	if err != nil || pending.Count != 1 {
		t.Fatalf("pending %+v %v", pending, err)
	}
	// This is the unique test project, not a production lease.
	b.r.Del(ctx, ReceiverKey(b.project, "dev"))
}

func TestCoordinationUnknownTrimmedTarget(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if _, err := b.Cmd(ctx, "sender", "peer", CmdDirective, "", "body"); err != nil {
		t.Fatal(err)
	}
	b.r.XGroupCreateMkStream(ctx, stream, "dev", "0")
	b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "dead", Streams: []string{stream, ">"}, Count: 1})
	b.r.XTrimMaxLen(ctx, stream, 0)
	if err := b.WatchCmdDelivery(ctx, "dev", "retry", "0", func(e Event) (bool, error) {
		if e.Target != "" || e.Message != "" || e.Delivery != "missing" {
			t.Fatalf("invented target/payload %+v", e)
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibilityWatchNeverCallsActionForMissingBody(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", "body"); err != nil {
		t.Fatal(err)
	}
	b.r.XGroupCreateMkStream(ctx, stream, "dev", "0")
	b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "dead", Streams: []string{stream, ">"}, Count: 1})
	b.r.XTrimMaxLen(ctx, stream, 0)
	err := b.WatchCmd(ctx, "dev", "new", "0", func(Event) bool { t.Fatal("legacy callback given non-actionable gap"); return true })
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatal(err)
	}
}

func TestInvalidCursorBeforeDial(t *testing.T) {
	b, _ := Open(nil, "test")
	for _, floor := range []string{"abc", "-1-0", "9223372036854775808-0"} {
		if err := b.WatchCmdDelivery(context.Background(), "dev", "consumer", floor, func(Event) (bool, error) { return true, nil }); err == nil {
			t.Fatalf("invalid cursor %q", floor)
		}
	}
}
