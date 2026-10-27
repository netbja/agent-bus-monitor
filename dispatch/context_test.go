package dispatch

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// The dispatcher publishes each verified agent's context fill, read from its
// own session only: an unverified snapshot or one without a session reads
// nothing; an agent without a reading is absent (unknown, never 0).
func TestContextPassPublishesVerifiedSessionsOnly(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "ctx-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	readings := map[string]quota.ContextReading{
		"s-coder":   {Pct: 84, ObservedAt: 111, Session: "s-coder"},
		"s-forged":  {Pct: 99, ObservedAt: 222, Session: "s-forged"},
		"s-nofile2": {Pct: 50, ObservedAt: 333, Session: "s-nofile2"},
	}
	var asked []string
	read := func(s string) (quota.ContextReading, bool) {
		asked = append(asked, s)
		r, ok := readings[s]
		return r, ok
	}
	snaps := map[string]bus.AgentSnapshot{
		"coder":    {PID: 10, Session: "s-coder", IdentSession: "s-coder"}, // verified, with a reading
		"forger":   {Session: "s-forged"},                                  // unverified: never read
		"master":   {PID: 11},                                              // verified, no session
		"sentinel": {PID: 12, Session: "s-none", IdentSession: "s-none"},   // verified, no reading
		// Verified, then an unverified status named another session: the
		// proved one is read, never the named one (foureyes' probe).
		"docs": {PID: 13, Session: "s-forged", IdentSession: "s-nofile2"},
	}
	l := &loop{b: b, token: "ctx-holder", cfg: Config{Log: io.Discard, Context: ContextConfig{Read: read, Every: time.Minute}}}
	if err := l.contextPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	fills, err := b.Context(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fills) != 2 || fills["docs"].Session != "s-nofile2" || fills["docs"].Pct != 50 || fills["coder"].Pct != 84 || fills["coder"].ObservedAt != 111 || fills["coder"].Session != "s-coder" || fills["coder"].At == 0 {
		t.Fatalf("published %+v", fills)
	}
	for _, s := range asked {
		if s == "s-forged" {
			t.Fatal("a session not proved by a verified snapshot was read")
		}
	}
	// Within the interval nothing is read again; a lost lease writes nothing.
	n := len(asked)
	if l.contextPass(ctx, snaps); len(asked) != n {
		t.Fatal("read again within the interval")
	}
	l2 := &loop{b: b, token: "not-the-holder", cfg: Config{Log: io.Discard, Context: ContextConfig{Read: read, Every: time.Minute}}}
	if err := l2.contextPass(ctx, snaps); err != bus.ErrNotHolder {
		t.Fatalf("a non-holder: %v", err)
	}
}

// No reader configured: nothing is read or written.
func TestContextPassOff(t *testing.T) {
	l := &loop{cfg: Config{Log: io.Discard}}
	if err := l.contextPass(context.Background(), map[string]bus.AgentSnapshot{"coder": {PID: 1, Session: "s"}}); err != nil {
		t.Fatal(err)
	}
}
