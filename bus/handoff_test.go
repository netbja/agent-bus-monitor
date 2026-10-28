package bus

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

// Voiding is durable (reviewer-codex's review of lifecycle-2): any non-idle
// status or any report the agent writes after its hand-off deletes the record
// in the same transaction, so working then idle before the dispatcher's next
// pass never makes a stale hand-off pending again. An idle status keeps it:
// it is what the agent declares right after its hand-off.
func TestHandoffVoidedDurablyByWorkOrAReportRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	self := AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7, Session: "session-0001"}
	declare := func() {
		t.Helper()
		r, err := b.Report(ctx, "coder", ReportNote, "hand-off")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.DeclareHandoff(ctx, "coder", r, self); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() bool {
		m, err := b.Handoffs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := m["coder"]
		return ok
	}
	status := func(state string) {
		t.Helper()
		if _, err := b.Status(ctx, "coder", state, "", self); err != nil {
			t.Fatal(err)
		}
	}

	declare()
	status("idle")
	if !pending() {
		t.Fatal("the idle status after a hand-off voided it")
	}
	status("working")
	status("idle") // before any dispatcher pass
	if pending() {
		t.Fatal("working then idle left the hand-off pending")
	}

	for _, st := range []string{"blocked", "waiting", "done"} {
		declare()
		status(st)
		if pending() {
			t.Errorf("a %s status left the hand-off pending", st)
		}
	}

	declare()
	if _, err := b.Report(ctx, "coder", ReportNote, "more work"); err != nil {
		t.Fatal(err)
	}
	if pending() {
		t.Fatal("a later report left the hand-off pending")
	}

	declare()
	if _, err := b.Report(ctx, "foureyes", ReportNote, "someone else"); err != nil {
		t.Fatal(err)
	}
	if !pending() {
		t.Fatal("another agent's report voided coder's hand-off")
	}
}

// The clear's reservation (lifecycle part B): lease-checked, only for the
// record judged, once per session; its mark is session|Redis TIME.
func TestFencedReserveClearRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	r, err := b.Report(ctx, "coder", ReportNote, "hand-off")
	if err != nil {
		t.Fatal(err)
	}
	h, err := b.DeclareHandoff(ctx, "coder", r, AgentIdent{PID: 42, PIDStart: 7, Session: "session-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, "coder", "idle", "hand-off declared", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.FencedReserveClear(ctx, "tok", "coder", h); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	stale := h
	stale.At--
	if _, ok, err := b.FencedReserveClear(ctx, "tok", "coder", stale); err != nil || ok {
		t.Fatalf("a record not the one stored was reserved: %v %v", ok, err)
	}
	at, ok, err := b.FencedReserveClear(ctx, "tok", "coder", h)
	if err != nil || !ok || at < h.At {
		t.Fatalf("reserve: %d %v %v", at, ok, err)
	}
	if _, ok, _ := b.FencedReserveClear(ctx, "tok", "coder", h); ok {
		t.Fatal("reserved twice for one session")
	}
	marks, _ := b.QuotaMarks(ctx)
	last, _ := b.r.XRevRangeN(ctx, StreamKey(b.project, "status"), "+", "-", 1).Result()
	if len(last) != 1 {
		t.Fatal("no status entry")
	}
	if m := marks[ClearMark("coder")]; m != "session-0001|"+strconv.FormatInt(at, 10)+"|"+r+"|"+last[0].ID+"|1" {
		t.Fatalf("mark %q", m)
	}
	c, ok := ParseClearMark(marks[ClearMark("coder")])
	if !ok || c.Session != "session-0001" || c.At != at || c.Report != r || c.StatusID != last[0].ID || c.Added != 1 {
		t.Fatalf("ParseClearMark: %+v %v", c, ok)
	}
}

// A status after the reservation's boundary is found to the sequence: two
// entries in one millisecond are told apart (reviewer-codex's review of
// lifecycle-4), and only the agent's own count. It fails closed when any
// entry added after the boundary was trimmed away: the stream's
// entries-added counter, taken with the boundary, says how many there were.
func TestStatusAfterIsSequenceExactRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	key := StreamKey(b.project, "status")
	for _, e := range []struct{ id, agent string }{{"1000-0", "coder"}, {"1000-1", "foureyes"}, {"1000-2", "coder"}} {
		if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: e.id, Values: map[string]any{"agent": e.agent, "state": "idle"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		agent, after string
		added        int64 // entries-added when the boundary was taken
		want         bool
	}{{"coder", "1000-0", 1, true}, {"coder", "1000-2", 3, false}, {"foureyes", "1000-1", 2, false}, {"foureyes", "1000-0", 1, true}, {"coder", "", 0, true}} {
		got, err := b.StatusAfter(ctx, c.agent, c.after, c.added)
		if err != nil || got != c.want {
			t.Errorf("StatusAfter(%s, %s, %d) = %v %v, want %v", c.agent, c.after, c.added, got, err, c.want)
		}
	}
	// Entries after the boundary trimmed away (reviewer-codex): fails closed,
	// for any agent.
	if err := b.r.XTrimMinID(ctx, key, "1000-2").Err(); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"coder", "foureyes"} {
		if got, err := b.StatusAfter(ctx, agent, "1000-0", 1); err != nil || !got {
			t.Errorf("trimmed after the boundary, %s: %v %v, want written", agent, got, err)
		}
	}
	// The boundary itself trimmed, nothing after it lost: nothing written.
	if got, err := b.StatusAfter(ctx, "foureyes", "1000-1", 2); err != nil || got {
		t.Errorf("only the boundary trimmed: %v %v, want nothing", got, err)
	}

	// An empty stream at the reservation (boundary 0-0): nothing since is nothing.
	fresh := dialTest(t)
	if got, err := fresh.StatusAfter(ctx, "foureyes", "0-0", 0); err != nil || got {
		t.Errorf("empty then and now: %v %v, want nothing", got, err)
	}
	// Empty at the reservation, then the agent's status trimmed away by others'.
	fk := StreamKey(fresh.project, "status")
	for _, a := range []string{"coder", "foureyes", "foureyes"} {
		if err := fresh.r.XAdd(ctx, &redis.XAddArgs{Stream: fk, Values: map[string]any{"agent": a, "state": "idle"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := fresh.r.XTrimMaxLen(ctx, fk, 2).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := fresh.StatusAfter(ctx, "coder", "0-0", 0); err != nil || !got {
		t.Errorf("empty at the reservation, then trimmed: %v %v, want written", got, err)
	}
}
