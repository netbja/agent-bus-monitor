package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A hand-off is declared by the agent itself (lifecycle spec, part A): its own
// report, from its verified process, in its proved session. Anything else
// writes nothing.
func TestDeclareHandoffNeedsTheAgentsOwnReportProcessAndSessionRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "session-0001", 100); err != nil {
		t.Fatalf("ProveSession: %v", err)
	}
	own, err := b.Report(ctx, "coder", ReportNote, "hand-off: branch x at abc, next: y")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := b.Report(ctx, "foureyes", ReportNote, "not coder's")
	if err != nil {
		t.Fatal(err)
	}
	ghosts, err := b.Report(ctx, "ghost", ReportNote, "a report, no snapshot")
	if err != nil {
		t.Fatal(err)
	}
	self := AgentIdent{PID: 42, PIDStart: 7, Session: "session-0001"}

	for name, c := range map[string]struct {
		agent, report string
		who           AgentIdent
		want          error
	}{
		"another agent's report": {"coder", foreign, self, ErrHandoffNotOwnReport},
		"no such report":         {"coder", "1-0", self, ErrHandoffNotOwnReport},
		"another process":        {"coder", own, AgentIdent{PID: 43, PIDStart: 7, Session: "session-0001"}, ErrHandoffNotVerified},
		"a recycled pid":         {"coder", own, AgentIdent{PID: 42, PIDStart: 8, Session: "session-0001"}, ErrHandoffNotVerified},
		"no process":             {"coder", own, AgentIdent{Session: "session-0001"}, ErrHandoffNotVerified},
		"another session":        {"coder", own, AgentIdent{PID: 42, PIDStart: 7, Session: "session-0002"}, ErrHandoffNotVerified},
		"no snapshot":            {"ghost", ghosts, self, ErrHandoffNotVerified},
	} {
		if _, err := b.DeclareHandoff(ctx, c.agent, c.report, c.who); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", name, err, c.want)
		}
	}
	if m, err := b.Handoffs(ctx); err != nil || len(m) != 0 {
		t.Fatalf("refused declarations wrote %v (%v)", m, err)
	}
	if _, err := b.DeclareHandoff(ctx, "Bad Name", own, self); err == nil {
		t.Fatal("accepted an invalid agent")
	}
	if _, err := b.DeclareHandoff(ctx, "coder", "x\"y", self); err == nil {
		t.Fatal("accepted a report id that is no stream id")
	}

	h, err := b.DeclareHandoff(ctx, "coder", own, self)
	if err != nil {
		t.Fatalf("DeclareHandoff: %v", err)
	}
	m, err := b.Handoffs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := m["coder"]
	if got != h || got.Report != own || got.Session != "session-0001" || got.PID != 42 || got.PIDStart != 7 || got.At == 0 {
		t.Fatalf("record %+v, returned %+v", got, h)
	}
	if ok, err := b.HasKeysBesidesDispatch(ctx); err != nil || !ok {
		t.Fatalf("HasKeysBesidesDispatch: %v %v", ok, err) // the agents' own keys are there
	}
}

// The void rule busmon and the dispatcher share: a record is void once the
// agent declared working after it, or its process or proved session changed.
func TestHandoffVoid(t *testing.T) {
	h := Handoff{Report: "5-0", Session: "session-0001", PID: 42, PIDStart: 7, At: 1000}
	snap := AgentSnapshot{State: "idle", TS: 1200, PID: 42, PIDStart: 7, IdentSession: "session-0001"}
	if void, why := h.VoidFor(snap, true); void {
		t.Fatalf("pending record void: %s", why)
	}
	for name, s := range map[string]func(AgentSnapshot) AgentSnapshot{
		"working after it": func(s AgentSnapshot) AgentSnapshot { s.State, s.TS = "working", 1001; return s },
		"another session":  func(s AgentSnapshot) AgentSnapshot { s.IdentSession = "session-0002"; return s },
		"another process":  func(s AgentSnapshot) AgentSnapshot { s.PID = 43; return s },
		"a recycled pid":   func(s AgentSnapshot) AgentSnapshot { s.PIDStart = 8; return s },
		"unverified":       func(s AgentSnapshot) AgentSnapshot { s.PID, s.PIDStart = 0, 0; return s },
	} {
		if void, why := h.VoidFor(s(snap), true); !void || why == "" {
			t.Errorf("%s: void %v (%q)", name, void, why)
		}
	}
	if void, _ := h.VoidFor(AgentSnapshot{}, false); !void {
		t.Error("no snapshot: not void")
	}
	// Working declared before the hand-off does not void it.
	if void, why := h.VoidFor(AgentSnapshot{State: "working", TS: 999, PID: 42, PIDStart: 7, IdentSession: "session-0001"}, true); void {
		t.Errorf("working before: void (%s)", why)
	}
}

// The dispatcher deletes a void record only while it holds the lease and the
// field still holds the record it judged: a newer declaration survives.
func TestFencedDeleteHandoffRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	self := AgentIdent{PID: 42, PIDStart: 7, Session: "session-0001"}
	declare := func() Handoff {
		r, err := b.Report(ctx, "coder", ReportNote, "hand-off")
		if err != nil {
			t.Fatal(err)
		}
		h, err := b.DeclareHandoff(ctx, "coder", r, self)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	old := declare()
	if err := b.FencedDeleteHandoff(ctx, "tok", "coder", old); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	newer := declare()
	if err := b.FencedDeleteHandoff(ctx, "tok", "coder", old); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.Handoffs(ctx); m["coder"] != newer {
		t.Fatalf("a stale judgement deleted the newer record: %v", m)
	}
	if err := b.FencedDeleteHandoff(ctx, "tok", "coder", newer); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.Handoffs(ctx); len(m) != 0 {
		t.Fatalf("not deleted: %v", m)
	}
}
