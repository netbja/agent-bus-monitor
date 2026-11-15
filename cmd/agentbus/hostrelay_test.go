package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

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
