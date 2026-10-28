package dispatch

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A void hand-off record is deleted by the dispatcher (lifecycle spec, part
// A); a pending one stays, and a dispatcher without the lease writes nothing.
func TestHandoffPassDeletesOnlyVoidRecords(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	registerSession(t, b, "coder", "w1:p2", "session-0001")
	rep, err := b.Report(ctx, "coder", bus.ReportNote, "hand-off")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclareHandoff(ctx, "coder", rep, bus.AgentIdent{PID: testPID, PIDStart: testStart, Session: "session-0001"}); err != nil {
		t.Fatalf("DeclareHandoff: %v", err)
	}
	l := &loop{b: b, token: "holder", last: map[string]string{}, cfg: Config{Log: io.Discard}}
	pass := func(token string) error {
		t.Helper()
		snaps, err := b.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		l.token = token
		return l.handoffPass(ctx, snaps)
	}
	if err := pass("holder"); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.Handoffs(ctx); len(m) != 1 {
		t.Fatalf("a pending record was deleted: %v", m)
	}

	// A /clear by hand: the same process proves a new session. Only the
	// dispatcher can see that the record no longer stands (a status or report
	// would have deleted it itself, bus/stream.go).
	if err := b.ProveSession(ctx, "coder", testPID, testStart, "session-0002", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := pass("not-the-holder"); err != bus.ErrNotHolder {
		t.Fatalf("non-holder: %v", err)
	}
	if m, _ := b.Handoffs(ctx); len(m) != 1 {
		t.Fatalf("a non-holder deleted: %v", m)
	}
	if err := pass("holder"); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.Handoffs(ctx); len(m) != 0 {
		t.Fatalf("a void record survived: %v", m)
	}
}
