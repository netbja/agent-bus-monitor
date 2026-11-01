package dispatch

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

// clearWorld: coder, a verified Claude Code agent in pane w1:p2, has
// declared its hand-off and is idle since; the fake pane is ready.
type clearWorld struct {
	t   *testing.T
	b   *bus.Bus
	r   *redis.Client
	rt  *fakeRT
	l   *loop
	rep string
}

const coderPane = "w1:p2"

func newClearWorld(t *testing.T, on bool) *clearWorld {
	t.Helper()
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "clear-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	who := bus.AgentIdent{Pane: coderPane, PID: testPID, PIDStart: testStart, Session: "session-0001", Client: "claude-code", Provider: "anthropic"}
	if _, err := b.Status(ctx, "coder", "working", "", who); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", testPID, testStart, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Report(ctx, "coder", bus.ReportNote, "hand-off: all committed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclareHandoff(ctx, "coder", rep, who); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // the idle status's id must be newer than the record's TIME
	if _, err := b.Status(ctx, "coder", "idle", "hand-off declared", who); err != nil {
		t.Fatal(err)
	}
	w := &clearWorld{t: t, b: b, r: r, rep: rep}
	w.rt = newFakeRT(func(string, int) Inspection { return readyIn() })
	w.rt.reserved = func(string) bool {
		m, _ := b.QuotaMarks(context.Background())
		_, ok := bus.ParseClearMark(m[bus.ClearMark("coder")])
		return ok
	}
	w.l = &loop{b: b, rt: w.rt, token: "clear-holder", last: map[string]string{},
		cfg: Config{EscalateTo: "master", Log: io.Discard, Clear: ClearConfig{On: on, Proof: 30 * time.Second}}}
	return w
}

func (w *clearWorld) pass() {
	w.t.Helper()
	snaps, err := w.b.Agents(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.l.clearPass(context.Background(), snaps); err != nil {
		w.t.Fatalf("clearPass: %v", err)
	}
}

func (w *clearWorld) clears() int {
	n := 0
	for _, p := range w.rt.Prompts() {
		if p.Text == ClearText {
			n++
		}
	}
	return n
}

// cmds are the dispatcher's cmd entries as target|wake|command.
func (w *clearWorld) cmds() []string {
	var out []string
	for _, m := range entries(w.t, w.r, w.b, "cmd") {
		if m.Values["from"] != dispatchFrom {
			continue
		}
		wake, _ := m.Values["wake"].(string)
		out = append(out, m.Values["target"].(string)+"|"+wake+"|"+m.Values["command"].(string))
	}
	return out
}

func (w *clearWorld) handoffs() int {
	m, err := w.b.Handoffs(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return len(m)
}

// The happy path: /clear once, the new proved session, the resume directive
// naming the report, the record gone, and no second clear.
func TestClearPassClearsProvesAndResumes(t *testing.T) {
	w := newClearWorld(t, true)
	w.pass()
	if w.clears() != 1 || len(w.cmds()) != 0 {
		t.Fatalf("clears %d, cmds %q", w.clears(), w.cmds())
	}
	if !w.l.cleared["coder"] {
		t.Fatal("the agent cleared this pass is not kept from a wake in the same pass")
	}
	w.pass() // not proved yet, not timed out: nothing
	if w.clears() != 1 || len(w.cmds()) != 0 {
		t.Fatalf("before the proof: clears %d, cmds %q", w.clears(), w.cmds())
	}
	if err := w.b.ProveSession(context.Background(), "coder", testPID, testStart, "session-0002", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	w.pass()
	got := w.cmds()
	if len(got) != 1 || !strings.HasPrefix(got[0], "coder||context cleared: re-run your boot steps") || !strings.Contains(got[0], "agentbus reports "+w.rep) {
		t.Fatalf("resume: %q", got)
	}
	if w.handoffs() != 0 {
		t.Fatal("the cleared record stayed")
	}
	w.pass()
	w.pass()
	if w.clears() != 1 || len(w.cmds()) != 1 {
		t.Fatalf("after: clears %d, cmds %q", w.clears(), w.cmds())
	}
}

// Off, nothing is typed (the default until the live checks pass).
func TestClearPassOffTypesNothing(t *testing.T) {
	w := newClearWorld(t, false)
	w.pass()
	w.pass()
	if w.clears() != 0 || len(w.cmds()) != 0 || w.handoffs() != 1 {
		t.Fatalf("off: clears %d, cmds %q, records %d", w.clears(), w.cmds(), w.handoffs())
	}
}

// No proof in time: one notice to the escalation target, no retry.
func TestClearPassEscalatesOnceWithoutAProof(t *testing.T) {
	w := newClearWorld(t, true)
	w.l.cfg.Clear.Proof = time.Millisecond
	w.pass()
	time.Sleep(5 * time.Millisecond)
	w.pass()
	w.pass()
	got := w.cmds()
	if w.clears() != 1 || len(got) != 1 || !strings.HasPrefix(got[0], "master|no|auto-clear of coder not confirmed") {
		t.Fatalf("clears %d, cmds %q", w.clears(), got)
	}
}

// A turn that wrote around the clear (here a report, which deletes the
// record): the rebuild directive instead of the resume, plus a notice.
func TestClearPassRebuildsAfterATurnTheHandoffDoesNotCover(t *testing.T) {
	w := newClearWorld(t, true)
	w.pass()
	if _, err := w.b.Report(context.Background(), "coder", bus.ReportNote, "one more thing"); err != nil {
		t.Fatal(err)
	}
	w.pass()
	got := w.cmds()
	if len(got) != 2 || !strings.HasPrefix(got[0], "coder||your context was cleared after a turn") || !strings.HasPrefix(got[1], "master|no|auto-clear: coder ran a turn") {
		t.Fatalf("cmds %q", got)
	}
	w.pass()
	if len(w.cmds()) != 2 || w.clears() != 1 {
		t.Fatalf("after: %q", w.cmds())
	}
}

// An open gate holds the hand-off: no clear, and one notice naming it.
func TestClearPassWaitsOnAGateAndSaysSoOnce(t *testing.T) {
	w := newClearWorld(t, true)
	if err := w.b.OpenChallenge(context.Background(), "coder", "ref-9", "foureyes|review x"); err != nil {
		t.Fatal(err)
	}
	w.pass()
	w.pass()
	got := w.cmds()
	if w.clears() != 0 || len(got) != 1 || !strings.HasPrefix(got[0], "master|no|auto-clear: coder declared a hand-off") || !strings.Contains(got[0], "ref-9") {
		t.Fatalf("clears %d, cmds %q", w.clears(), got)
	}
}

// A dispatcher without the lease reserves nothing and types nothing.
func TestClearPassWithoutTheLeaseTypesNothing(t *testing.T) {
	w := newClearWorld(t, true)
	w.l.token = "not-the-holder"
	snaps, _ := w.b.Agents(context.Background())
	if err := w.l.clearPass(context.Background(), snaps); err != bus.ErrNotHolder {
		t.Fatalf("non-holder: %v", err)
	}
	if w.clears() != 0 {
		t.Fatal("typed without the lease")
	}
}

// An idle status written after the reservation in the same millisecond
// (reviewer-codex's review of lifecycle-4): the boundary is the status
// stream's id, so it still reads as a turn that ran, and the rebuild
// directive goes, not the resume.
func TestClearPassTellsASameMillisecondStatusApart(t *testing.T) {
	w := newClearWorld(t, true)
	ctx := context.Background()
	w.pass()
	who := bus.AgentIdent{Pane: coderPane, PID: testPID, PIDStart: testStart, Session: "session-0001", Client: "claude-code", Provider: "anthropic"}
	id, err := w.b.Status(ctx, "coder", "idle", "a queued turn ran", who)
	if err != nil {
		t.Fatal(err)
	}
	// Put the reservation's time in that status's millisecond, its boundary unchanged.
	marks, _ := w.b.QuotaMarks(ctx)
	c, ok := bus.ParseClearMark(marks[bus.ClearMark("coder")])
	if !ok {
		t.Fatal("no reservation")
	}
	ms, _, _ := strings.Cut(id, "-")
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), bus.ClearMark("coder"), c.Session+"|"+ms+"|"+c.Report+"|"+c.StatusID+"|"+strconv.FormatInt(c.Added, 10))
	if err := w.b.ProveSession(ctx, "coder", testPID, testStart, "session-0002", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	w.pass()
	got := w.cmds()
	if len(got) != 2 || !strings.HasPrefix(got[0], "coder||your context was cleared after a turn") {
		t.Fatalf("cmds %q", got)
	}
}

// The re-inspection right before typing (foureyes' review of lifecycle-4):
// a pane focused, busy or no longer ready since the first read gets no
// /clear, the reservation is released, and the next pass judges again.
func TestClearPassReinspectsRightBeforeTyping(t *testing.T) {
	for name, change := range map[string]func(*Inspection){
		"focused":   func(in *Inspection) { in.Focused = true },
		"busy":      func(in *Inspection) { in.Busy = true },
		"not ready": func(in *Inspection) { in.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			w := newClearWorld(t, true)
			var log strings.Builder
			w.l.cfg.Log = &log
			w.rt.script = func(_ string, n int) Inspection {
				in := readyIn()
				if n == 2 { // the second read of the first pass: just before typing
					change(&in)
				}
				return in
			}
			w.pass()
			if w.clears() != 0 || !strings.Contains(log.String(), "clear-aborted") {
				t.Fatalf("clears %d, log %q", w.clears(), log.String())
			}
			if m, _ := w.b.QuotaMarks(context.Background()); m[bus.ClearMark("coder")] != "" {
				t.Fatalf("the reservation was kept: %q", m[bus.ClearMark("coder")])
			}
			w.pass() // ready again: judged again and cleared
			if w.clears() != 1 {
				t.Fatalf("next pass: clears %d", w.clears())
			}
		})
	}
}

// A gate opened over 24 h ago does not hold the hand-off (rule 4): stale
// gates pile up and would hold it forever.
func TestClearPassIgnoresAStaleGate(t *testing.T) {
	w := newClearWorld(t, true)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	if err := w.r.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(w.b.Project(), "cmd"), ID: fmt.Sprintf("%d-1", old),
		Values: map[string]any{"from": "foureyes", "target": "coder", "type": bus.CmdChallenge, "ref": "ref-old", "command": "review"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := w.b.OpenChallenge(ctx, "coder", "ref-old", "foureyes|review"); err != nil {
		t.Fatal(err)
	}
	w.pass()
	if w.clears() != 1 || len(w.cmds()) != 0 {
		t.Fatalf("a two-day-old gate held it: clears %d, cmds %q", w.clears(), w.cmds())
	}
}

// A post-reservation status trimmed away by the capped stream before the
// proof (reviewer-codex): the reservation's boundary is gone, history after
// it is incomplete, and the proof fails closed: rebuild, never resume.
func TestClearPassFailsClosedWhenTheStatusHistoryWasTrimmed(t *testing.T) {
	w := newClearWorld(t, true)
	ctx := context.Background()
	w.pass()
	who := bus.AgentIdent{Pane: coderPane, PID: testPID, PIDStart: testStart, Session: "session-0001", Client: "claude-code", Provider: "anthropic"}
	if _, err := w.b.Status(ctx, "coder", "idle", "a queued turn ran", who); err != nil {
		t.Fatal(err)
	}
	// Other agents' statuses push both the boundary and that status out of the stream.
	key := bus.StreamKey(w.b.Project(), "status")
	last, err := w.b.Status(ctx, "foureyes", "idle", "", bus.AgentIdent{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.r.XTrimMinID(ctx, key, last).Err(); err != nil {
		t.Fatal(err)
	}
	if err := w.b.ProveSession(ctx, "coder", testPID, testStart, "session-0002", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	w.pass()
	got := w.cmds()
	if len(got) != 2 || !strings.HasPrefix(got[0], "coder||your context was cleared after a turn") {
		t.Fatalf("cmds %q", got)
	}
}

// A deferred clear says why: the clear pass keeps its last reason per agent
// (read by the clear-stall health check) and logs it once per change, never
// once per pass (stall guard, the 2026-11-01 silent hand-off).
func TestClearPassRecordsAndLogsWhyItDefers(t *testing.T) {
	w := newClearWorld(t, true)
	var log strings.Builder
	w.l.cfg.Log = &log
	w.l.cfg.Clear.Decisions = NewClearDecisions()
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Focused = true; return in }
	w.pass()
	w.pass()
	if got := w.l.cfg.Clear.Decisions.Last("coder"); got != "the pane is focused" {
		t.Fatalf("last decision %q", got)
	}
	if n := strings.Count(log.String(), `"clear-deferred"`); n != 1 {
		t.Fatalf("%d clear-deferred lines over two passes:\n%s", n, log.String())
	}
	if !strings.Contains(log.String(), `"reason":"the pane is focused"`) {
		t.Fatalf("the log does not say why:\n%s", log.String())
	}
	w.rt.script = func(string, int) Inspection { return readyIn() }
	w.pass() // unfocused: cleared, and the record says so
	if w.clears() != 1 || w.l.cfg.Clear.Decisions.Last("coder") != "cleared" {
		t.Fatalf("clears %d, last %q", w.clears(), w.l.cfg.Clear.Decisions.Last("coder"))
	}
}

// Nil decisions (tests, the foreground dispatcher) are safe.
func TestClearDecisionsNilIsSafe(t *testing.T) {
	var d *ClearDecisions
	if d.note("coder", "x") || d.Last("coder") != "" {
		t.Fatal("a nil ClearDecisions records something")
	}
}

// The whole chain end to end (chain-reliability): an agent that declared idle
// just before its hand-off, and nothing after it (the person unblocked this by
// hand three times), is cleared, proved, told to resume, and drains the
// resume directive; nothing is left owed and nothing is typed twice.
func TestHandoffClearResumeDrainEndToEnd(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "clear-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	who := bus.AgentIdent{Pane: coderPane, PID: testPID, PIDStart: testStart, Session: "session-0001", Client: "claude-code", Provider: "anthropic"}
	if err := b.ProveSession(ctx, "coder", testPID, testStart, "session-0001", 100); err == nil {
		t.Fatal("proved a session with no snapshot")
	}
	if _, err := b.Status(ctx, "coder", "idle", "all committed", who); err != nil { // idle BEFORE the hand-off
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", testPID, testStart, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Report(ctx, "coder", bus.ReportNote, "hand-off: all committed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclareHandoff(ctx, "coder", rep, who); err != nil { // and no status after it
		t.Fatal(err)
	}
	rt := newFakeRT(func(string, int) Inspection { return readyIn() })
	rt.reserved = func(string) bool {
		m, _ := b.QuotaMarks(context.Background())
		_, ok := bus.ParseClearMark(m[bus.ClearMark("coder")])
		return ok
	}
	l := &loop{b: b, rt: rt, token: "clear-holder", last: map[string]string{},
		cfg: Config{EscalateTo: "master", Log: io.Discard, Clear: ClearConfig{On: true, Proof: 30 * time.Second}}}
	pass := func() {
		t.Helper()
		snaps, err := b.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.clearPass(ctx, snaps); err != nil {
			t.Fatalf("clearPass: %v", err)
		}
	}
	clears := func() int {
		n := 0
		for _, p := range rt.Prompts() {
			if p.Text == ClearText {
				n++
			}
		}
		return n
	}
	pass()
	if clears() != 1 {
		t.Fatalf("not cleared: %d clears", clears())
	}
	// The new session starts (SessionStart's prove-session), then the resume.
	if err := b.ProveSession(ctx, "coder", testPID, testStart, "session-0002", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	pass()
	var got []string
	if _, err := b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(e bus.Event) error {
		got = append(got, e.From+": "+e.Message)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], dispatchFrom+": context cleared: re-run your boot steps") || !strings.Contains(got[0], "agentbus reports "+rep) {
		t.Fatalf("drained %q", got)
	}
	if o, err := b.Obligation(ctx, "coder", bus.DefaultHorizon); err != nil || o.Wakes() {
		t.Fatalf("still owed after the drain: %+v %v", o, err)
	}
	pass()
	pass()
	if clears() != 1 || len(entries(t, r, b, "cmd")) != 1 {
		t.Fatalf("after: %d clears, %d cmd entries", clears(), len(entries(t, r, b, "cmd")))
	}
	if m, _ := b.Handoffs(ctx); len(m) != 0 {
		t.Fatalf("the record stayed: %v", m)
	}
}
