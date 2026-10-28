package dispatch

import (
	"context"
	"io"
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
		_, _, _, ok := bus.ParseClearMark(m[bus.ClearMark("coder")])
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
