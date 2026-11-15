package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

// answer plays the dispatcher once: it reads the directives and accepts
// (or refuses) each one, as dispatch's reader does.
func answer(t *testing.T, b *bus.Bus, refuse string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ops, _, err := b.HostOpsAfter(ctx, "0-0")
		if err != nil {
			t.Error(err)
			return
		}
		if len(ops) > 0 {
			if refuse != "" {
				if err := b.FencedHostRefuse(ctx, "relay", ops[0], refuse); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := b.FencedHostAccept(ctx, "relay", ops[0], bus.HostAcceptWindow); err != nil {
				t.Error(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("no directive to answer")
}

// herdr exit §4.2: the command waits for the dispatcher's reply, never for
// the operation: accepted is exit 0, a refusal exit 1 with its text, and no
// reply by the deadline (Redis TIME) the spec's failure, after one last read.
func TestRelayHostOp(t *testing.T) {
	ctx := context.Background()
	op := bus.HostOp{Op: bus.HostRelaunch, Agent: "coder", From: "master"}
	run := func(b *bus.Bus, window time.Duration) (int, string) {
		var out, errw bytes.Buffer
		code := relayHostOp(ctx, b, op, window, &out, &errw)
		return code, out.String() + errw.String()
	}
	for _, c := range []struct {
		refuse string
		code   int
		want   string
	}{
		{"", 0, "relaunch coder: accepted by the dispatcher"},
		{"master may not relaunch sentinel", 1, "relaunch coder: refused: master may not relaunch sentinel"},
	} {
		b, _ := dialMain(t)
		if ok, err := b.AcquireDispatch(ctx, "relay", time.Minute); err != nil || !ok {
			t.Fatalf("AcquireDispatch %v %v", ok, err)
		}
		go answer(t, b, c.refuse)
		if code, msg := run(b, bus.HostAcceptWindow); code != c.code || !strings.Contains(msg, c.want) {
			t.Fatalf("%q: %d %q", c.refuse, code, msg)
		}
	}
	b, _ := dialMain(t)
	start := time.Now()
	code, msg := run(b, 300*time.Millisecond)
	if code != 1 || !strings.Contains(msg, relayFailText) || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("no dispatcher: %d %q after %s", code, msg, time.Since(start))
	}
	if err := (bus.HostOp{Op: "exec", From: "master"}).Validate(); err == nil {
		t.Fatal("exec accepted")
	}
	var out, errw bytes.Buffer
	if code := relayHostOp(ctx, b, bus.HostOp{Op: "exec", From: "master"}, time.Second, &out, &errw); code != 1 {
		t.Fatalf("an op outside the set sent: %d", code)
	}
}

// §4.2 (foureyes): a reply first seen by the last read, once the deadline
// has passed by Redis TIME, is the answer: no failure is declared.
func TestRelayHostOpLastRead(t *testing.T) {
	ctx := context.Background()
	b, _ := dialMain(t)
	if ok, err := b.AcquireDispatch(ctx, "relay", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	real := hostReply
	defer func() { hostReply = real }()
	reads := 0
	hostReply = func(ctx context.Context, b *bus.Bus, id, to string) (string, bool, error) {
		reads++
		if reads == 1 { // before the deadline: nothing yet; the dispatcher answers late
			ops, _, _ := b.HostOpsAfter(ctx, "0-0")
			if err := b.FencedHostRefuse(ctx, "relay", ops[0], "too late to tell"); err != nil {
				t.Error(err)
			}
			time.Sleep(60 * time.Millisecond)
			return "", false, nil
		}
		return real(ctx, b, id, to)
	}
	var out, errw bytes.Buffer
	code := relayHostOp(ctx, b, bus.HostOp{Op: bus.HostUp, From: "master"}, 50*time.Millisecond, &out, &errw)
	if code != 1 || reads != 2 || !strings.Contains(errw.String(), "up: refused: too late to tell") || strings.Contains(errw.String(), relayFailText) {
		t.Fatalf("%d after %d reads: %q", code, reads, errw.String())
	}
}

// security S10a-1: a forged entry an hour ahead pushes every later id
// forward; the command's deadline is still 5 s by Redis TIME at its send,
// not its own id's: no dispatcher, it fails within the window.
func TestRelayHostOpDeadlineFromTheSend(t *testing.T) {
	ctx := context.Background()
	b, r := dialMain(t)
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	future := strconv.FormatInt(now.Add(time.Hour).UnixMilli(), 10) + "-0"
	if err := r.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(b.Project(), "cmd"), ID: future,
		Values: map[string]interface{}{"from": "coder", "target": "master", "type": "directive", "command": "x"}}).Err(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var out, errw bytes.Buffer
	code := relayHostOp(ctx, b, bus.HostOp{Op: bus.HostUp, From: "master"}, 300*time.Millisecond, &out, &errw)
	if code != 1 || !strings.Contains(errw.String(), relayFailText) || time.Since(start) > 5*time.Second {
		t.Fatalf("%d after %s: %q", code, time.Since(start), errw.String())
	}
}

// reviewer-codex on 51c06dd7: once the command has said nothing was done,
// the dispatcher can no longer act on the directive, however late it reads
// it (a delayed XADD, a dispatcher that comes back); and a directive the
// dispatcher accepted just before the give-up is reported accepted.
func TestRelayHostOpGiveUpIsFinal(t *testing.T) {
	ctx := context.Background()
	b, _ := dialMain(t)
	if ok, err := b.AcquireDispatch(ctx, "relay", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	var out, errw bytes.Buffer
	if code := relayHostOp(ctx, b, bus.HostOp{Op: bus.HostUp, From: "master"}, 100*time.Millisecond, &out, &errw); code != 1 || !strings.Contains(errw.String(), relayFailText) {
		t.Fatalf("no dispatcher: %d %q", code, errw.String())
	}
	ops, _, err := b.HostOpsAfter(ctx, "0-0")
	if err != nil || len(ops) != 1 {
		t.Fatalf("ops %v %v", ops, err)
	}
	if st, err := b.FencedHostAccept(ctx, "relay", ops[0], time.Hour); err != nil || st != bus.HostSeen {
		t.Fatalf("a late accept after the give-up = %q, %v", st, err)
	}
	// Accepted between the last read and the give-up: reported accepted.
	real := hostReply
	defer func() { hostReply = real }()
	hostReply = func(ctx context.Context, b *bus.Bus, id, to string) (string, bool, error) {
		ops, _, _ := b.HostOpsAfter(ctx, ops[0].ID)
		for _, o := range ops {
			if o.ID == id {
				if _, err := b.FencedHostAccept(ctx, "relay", o, time.Hour); err != nil {
					t.Error(err)
				}
			}
		}
		return "", false, nil // the reads see nothing; only the withdrawal tells
	}
	out.Reset()
	errw.Reset()
	if code := relayHostOp(ctx, b, bus.HostOp{Op: bus.HostUp, From: "master"}, 100*time.Millisecond, &out, &errw); code != 0 || !strings.Contains(out.String(), "accepted") {
		t.Fatalf("accepted before the give-up: %d %q %q", code, out.String(), errw.String())
	}
}
