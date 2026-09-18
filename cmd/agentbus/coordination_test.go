package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

type failedWriter struct{ short bool }

func (w failedWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) / 2, nil
	}
	return 0, io.ErrClosedPipe
}

type interruptAfterWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *interruptAfterWrite) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestSubscribeOutputFailureAndRecovery(t *testing.T) {
	for _, mode := range []string{"closed", "short", "interrupted-after-write"} {
		t.Run(mode, func(t *testing.T) {
			b, r := dialMain(t)
			ctx := context.Background()
			body := strings.Repeat("é漢🙂\n  line\t", 1000)
			id, err := b.Cmd(ctx, "sender", "dev", bus.CmdDirective, "", body)
			if err != nil {
				t.Fatal(err)
			}
			c, cancel := context.WithCancel(ctx)
			defer cancel()
			var out io.Writer = failedWriter{short: mode == "short"}
			var interrupted *interruptAfterWrite
			if mode == "interrupted-after-write" {
				interrupted = &interruptAfterWrite{cancel: cancel}
				out = interrupted
			}
			code := runSubscribe(c, b, "dev", "old-process", time.Second, "0", false, out)
			if code != 75 {
				t.Fatalf("output failure exit %d", code)
			}
			if interrupted != nil {
				ev := lastEvent(t, interrupted.String())
				if ev.Body != body || strings.Count(interrupted.String(), "\n") != 1 {
					t.Fatal("not exactly one complete JSON object")
				}
			}
			p, err := r.XPending(ctx, bus.StreamKey(b.Project(), "cmd"), "dev").Result()
			if err != nil || p.Count != 1 {
				t.Fatalf("lost pending %v %v", p, err)
			}
			var retry bytes.Buffer
			code = runSubscribe(ctx, b, "dev", "new-process", time.Second, "0", false, &retry)
			if code != 0 {
				t.Fatalf("recovery exit %d: %s", code, retry.String())
			}
			ev := lastEvent(t, retry.String())
			if ev.ID != id || ev.Body != body || !ev.DuplicatePossible || ev.Attempt != 2 || ev.Delivery != "uncertain" {
				t.Fatalf("incorrect recovery metadata or body, id=%s duplicate=%v attempt=%d", ev.ID, ev.DuplicatePossible, ev.Attempt)
			}
		})
	}
}

func TestSubscribeUnavailableIsNotCommand(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	id, err := b.Cmd(ctx, "sender", "dev", bus.CmdDirective, "", "unsafe")
	if err != nil {
		t.Fatal(err)
	}
	r.XGroupCreateMkStream(ctx, stream, "dev", "0")
	r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "dev", Consumer: "dead", Streams: []string{stream, ">"}, Count: 1})
	r.XTrimMaxLen(ctx, stream, 0)
	var out bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "new", time.Second, "0", false, &out)
	ev := lastEvent(t, out.String())
	if code != 75 || ev.Event != "error" || ev.Body != "" || ev.ID != id || ev.Delivery != "missing" || ev.Rearm == nil || !*ev.Rearm {
		t.Fatalf("unsafe missing event %+v code=%d", ev, code)
	}
}

func TestRequestCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.BoardKey(b.Project()))
	var out bytes.Buffer
	if err := runRequest(ctx, b, "sender", []string{"send", "task", "dev", "--ref", "thread", "--ttl", "1m", "do\nthis"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("no request ID")
	}
	if err := runRequest(ctx, b, "peer", []string{"accept", "task"}, io.Discard); err == nil {
		t.Fatal("wrong target accepted")
	}
	if err := runRequest(ctx, b, "dev", []string{"accept", "task"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runRequest(ctx, b, "dev", []string{"done", "task", "proof\n✓"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runRequest(ctx, b, "sender", []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"accepted_at":`, `"response_id":`, `"responded_at":`, `"state":"done"`, `"thread":"thread"`} {
		if !strings.Contains(out.String(), field) {
			t.Fatalf("missing %s: %s", field, out.String())
		}
	}
	if err := runRequest(ctx, b, "sender", []string{"--json"}, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error ignored: %v", err)
	}
}

// This helper is re-executed as a separate OS process by the test below. Exit
// inside Write deliberately bypasses every defer, as a killed subscriber would.
func TestCoordinationSubscriberHelper(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_SUBSCRIBER") != "1" {
		return
	}
	client, err := bus.Connect("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(88)
	}
	b, err := bus.Open(client, os.Getenv("AGENTBUS_TEST_PROJECT"))
	if err != nil {
		os.Exit(89)
	}
	var out io.Writer = os.Stdout
	if phase := os.Getenv("AGENTBUS_TEST_CRASH"); phase != "" {
		out = processCrashWriter{after: phase == "after"}
	}
	code := runSubscribe(context.Background(), b, "dev", "child-process", time.Second, "0", false, out)
	os.Exit(code)
}

type processCrashWriter struct{ after bool }

func (w processCrashWriter) Write(p []byte) (int, error) {
	if w.after {
		_, _ = os.Stdout.Write(p)
	}
	os.Exit(90)
	return 0, nil
}

func TestSubscribeProcessCrashAndRestart(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			b, r := dialMain(t)
			ctx := context.Background()
			id, err := b.Cmd(ctx, "sender", "dev", bus.CmdDirective, "", "must survive\n✓")
			if err != nil {
				t.Fatal(err)
			}
			child := func(crash string) ([]byte, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestCoordinationSubscriberHelper$")
				cmd.Env = append(os.Environ(), "AGENTBUS_TEST_SUBSCRIBER=1", "AGENTBUS_TEST_PROJECT="+b.Project(), "AGENTBUS_TEST_CRASH="+crash)
				return cmd.CombinedOutput()
			}
			output, err := child(phase)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 90 {
				t.Fatalf("child did not crash at boundary: %v %s", err, output)
			}
			if phase == "after" {
				ev := lastEvent(t, string(output))
				if ev.ID != id {
					t.Fatal(ev)
				}
			} else if len(output) != 0 {
				t.Fatalf("output before write %s", output)
			}
			ttl, err := r.PTTL(ctx, bus.ReceiverKey(b.Project(), "dev")).Result()
			if err != nil || ttl <= 0 {
				t.Fatalf("crash unexpectedly ran cleanup: %v %v", ttl, err)
			}
			// Accelerate only this throwaway lease's expiry, then restart a new process.
			if err := r.PExpire(ctx, bus.ReceiverKey(b.Project(), "dev"), time.Millisecond).Err(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
			output, err = child("")
			if err != nil {
				t.Fatalf("restart %v %s", err, output)
			}
			ev := lastEvent(t, string(output))
			if ev.ID != id || !ev.DuplicatePossible || ev.Body != "must survive\n✓" {
				t.Fatalf("restart lost/didn't flag duplicate: %+v", ev)
			}
			// Successful output+ACK on the prior process: restart delivers only NEW work.
			newID, err := b.Cmd(ctx, "sender", "dev", bus.CmdDirective, "", "new work")
			if err != nil {
				t.Fatal(err)
			}
			output, err = child("")
			if err != nil {
				t.Fatalf("second restart %v %s", err, output)
			}
			ev = lastEvent(t, string(output))
			if ev.ID != newID || ev.DuplicatePossible {
				t.Fatalf("already ACKed command replayed %+v", ev)
			}
		})
	}
}

func TestSubscribeContendedLeaseWaitsForIdle(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	key := bus.ReceiverKey(b.Project(), "dev")
	if err := r.Set(ctx, key, "other-subscriber", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	defer r.Del(ctx, key)
	start := time.Now()
	var out bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "waiting", 200*time.Millisecond, "0", false, &out)
	if time.Since(start) < 190*time.Millisecond {
		t.Fatal("contention woke session immediately")
	}
	ev := lastEvent(t, out.String())
	if code != 64 || ev.Event != "heartbeat" {
		t.Fatalf("contention must idle, got %d %+v", code, ev)
	}
	if v := r.Get(ctx, key).Val(); v != "other-subscriber" {
		t.Fatal("waiter disturbed owner")
	}
}

func TestSubscribeTakesOverWhenLeaseExpires(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	key := bus.ReceiverKey(b.Project(), "dev")
	if _, err := b.Cmd(ctx, "sender", "dev", bus.CmdDirective, "", "body"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, key, "dead-subscriber", 100*time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runSubscribe(ctx, b, "dev", "new", time.Second, "0", false, &out); code != 0 {
		t.Fatalf("takeover failed: %d %s", code, out.String())
	}
}
