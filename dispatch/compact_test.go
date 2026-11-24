package dispatch

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// compactable is a fact set DecideCompact passes: claude-code at 92% for 11
// minutes above the urgent 90, no hand-off, its pane ready and unfocused.
func compactable() CompactFacts {
	return CompactFacts{On: true, Urgent: 90, After: 10 * time.Minute,
		Snap:  bus.AgentSnapshot{Client: "claude-code", PID: testPID, PIDStart: testStart},
		Fill:  bus.ContextFill{Pct: 92, Session: "s1", At: 20 * 60000},
		Since: 9 * 60000, Inspection: readyIn()}
}

// auto-compact: the rule, cheapest first; every guard alone holds the /compact.
func TestDecideCompact(t *testing.T) {
	if d := DecideCompact(compactable()); !d.Compact || d.Reason != "" {
		t.Fatalf("compactable: %+v", d)
	}
	cases := map[string]func(*CompactFacts){
		"auto-compact is off":                       func(f *CompactFacts) { f.On = false },
		"client codex has no known /compact":        func(f *CompactFacts) { f.Snap.Client = "codex" },
		"no urgent threshold":                       func(f *CompactFacts) { f.Urgent = 0 },
		"below the urgent fill":                     func(f *CompactFacts) { f.Fill.Pct = 89.9 },
		"at the urgent fill for less than 10m0s":    func(f *CompactFacts) { f.Since = f.Fill.At - 10*60000 + 1 },
		"not yet seen at the urgent fill":           func(f *CompactFacts) { f.Since = 0 },
		"its hand-off stands: the auto-clear's job": func(f *CompactFacts) { f.Standing = true },
		"already compacted in this episode":         func(f *CompactFacts) { f.Marked = true },
		"a swap or reload is in progress":           func(f *CompactFacts) { f.Swapping = true },
		"its provider is held":                      func(f *CompactFacts) { f.Held = true },
		"a wake is being typed":                     func(f *CompactFacts) { f.WakeState = "prompting" },
		"inspection failed: boom":                   func(f *CompactFacts) { f.Inspection.Err = errors.New("boom") },
		"its verified process is not the pane's":    func(f *CompactFacts) { f.Snap.PIDStart = 1 },
		"the pane is focused":                       func(f *CompactFacts) { f.Inspection.Focused = true },
		"the pane is blocked on a prompt":           func(f *CompactFacts) { f.Inspection.State = "blocked" },
		"the pane is not ready":                     func(f *CompactFacts) { f.Inspection.Ready = false },
		"the pane is busy":                          func(f *CompactFacts) { f.Inspection.Busy = true },
	}
	for want, mut := range cases {
		f := compactable()
		mut(&f)
		d := DecideCompact(f)
		if want == "the pane is busy" {
			want = "the pane is not ready"
		}
		if d.Compact || d.Reason != want {
			t.Errorf("%s: got %+v", want, d)
		}
	}
	// Exactly the hold: ten minutes to the millisecond compacts.
	f := compactable()
	f.Since = f.Fill.At - 10*60000
	if !DecideCompact(f).Compact {
		t.Error("ten minutes at the urgent fill did not compact")
	}
	if !strings.Contains(DecideCompact(CompactFacts{}).Reason, "off") {
		t.Error("the zero facts compacted")
	}
}

// cs is the compact tests' proved session.
const cs = "session-0001"

// compactWorld is escalateWorld (alert 80, urgent 90) with coder a verified
// claude-code agent in p-coder, a fake pane and auto-compact on.
type compactWorld struct {
	*escalateWorld
	rt *fakeRT
	on bool
}

func newCompactWorld(t *testing.T, in func(string, int) Inspection) *compactWorld {
	t.Helper()
	w := &compactWorld{escalateWorld: newEscalateWorld(t, 90, 0), rt: newFakeRT(in), on: true}
	if _, err := w.b.Status(w.ctx, "coder", "idle", "", bus.AgentIdent{Pane: "p-coder", PID: testPID, PIDStart: testStart, Session: cs, Client: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := w.b.ProveSession(w.ctx, "coder", testPID, testStart, cs, 100); err != nil {
		t.Fatal(err)
	}
	w.rt.compactReserved = func(pane string) bool {
		marks, _ := w.b.QuotaMarks(w.ctx)
		return pane == "p-coder" && marks[bus.CompactMark("coder")] == cs
	}
	w.l.rt = w.rt
	w.l.cfg.Context.Compact = func() bool { return w.on }
	return w
}

// pass is escalateWorld.pass in session cs.
func (w *compactWorld) pass(pct float64) {
	w.t.Helper()
	w.mu.Lock()
	w.pct[cs] = pct
	w.mu.Unlock()
	snaps := map[string]bus.AgentSnapshot{"coder": {PID: testPID, PIDStart: testStart, IdentSession: cs}}
	if err := w.l.contextPass(w.ctx, snaps); err != nil {
		w.t.Fatal(err)
	}
	w.l.contextAt = time.Time{}
	if err := w.l.contextAlerts(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// ageHold moves the urgent hold's start back by d.
func (w *compactWorld) ageHold(d time.Duration) {
	w.t.Helper()
	marks, _ := w.b.QuotaMarks(w.ctx)
	at := compactSince(marks, "coder", cs)
	if at == 0 {
		w.t.Fatal("no urgent hold recorded")
	}
	v := cs + "|" + strconv.FormatInt(at-d.Milliseconds(), 10)
	if err := w.r.HSet(w.ctx, bus.QuotaKey(w.b.Project()), bus.CompactSinceMark("coder"), v).Err(); err != nil {
		w.t.Fatal(err)
	}
}

func (w *compactWorld) compacts() int {
	n := 0
	for _, p := range w.rt.Prompts() {
		if p.Text == "/compact" {
			n++
		}
	}
	return n
}

// auto-compact: at 90% for 10 minutes with no hand-off, /compact once, master
// told once without waking; the episode's end forgets it all.
func TestAutoCompactTypesOnceAfterTheHold(t *testing.T) {
	w := newCompactWorld(t, alwaysReady)
	w.pass(92)
	if w.compacts() != 0 {
		t.Fatal("compacted at the first urgent reading")
	}
	w.ageHold(9 * time.Minute)
	w.pass(92)
	if w.compacts() != 0 {
		t.Fatal("compacted before ten minutes")
	}
	w.ageHold(time.Minute)
	w.pass(93)
	w.pass(94)
	if n := w.compacts(); n != 1 {
		t.Fatalf("%d /compact typed, want 1", n)
	}
	if n := w.count("master", "auto-compact: coder at 93% for 10 min", false); n != 1 {
		t.Fatalf("%d wake=no notices to master, want 1", n)
	}
	if n := w.count("master", "auto-compact", true); n != 0 {
		t.Fatalf("%d waking auto-compact directives", n)
	}
	w.pass(30) // the episode ends: its marks go, the next rise may compact again
	marks, _ := w.b.QuotaMarks(w.ctx)
	for _, k := range []string{bus.CompactSinceMark("coder"), bus.CompactMark("coder"), bus.CompactNoticeMark("coder")} {
		if _, ok := marks[k]; ok {
			t.Errorf("%s outlived the episode", k)
		}
	}
}

// A fill that falls below the urgent threshold restarts the hold.
func TestAutoCompactHoldRestartsBelowUrgent(t *testing.T) {
	w := newCompactWorld(t, alwaysReady)
	w.pass(92)
	w.ageHold(11 * time.Minute)
	w.pass(85)
	if marks, _ := w.b.QuotaMarks(w.ctx); marks[bus.CompactSinceMark("coder")] != "" {
		t.Fatal("the hold outlived a reading below urgent")
	}
	w.pass(92)
	if w.compacts() != 0 {
		t.Fatal("compacted on a restarted hold")
	}
}

// Off, focused, a standing hand-off: nothing typed, nothing reserved; on
// again and unfocused, the next pass compacts.
func TestAutoCompactGuards(t *testing.T) {
	focused := true
	w := newCompactWorld(t, func(string, int) Inspection {
		in := readyIn()
		in.Focused = focused
		return in
	})
	w.on = false
	w.pass(92)
	w.ageHold(11 * time.Minute)
	w.pass(92)
	w.on = true
	w.pass(92)
	if w.compacts() != 0 {
		t.Fatal("compacted while off or focused")
	}
	if marks, _ := w.b.QuotaMarks(w.ctx); marks[bus.CompactMark("coder")] != "" {
		t.Fatal("a held /compact kept its reservation")
	}
	rep, err := w.b.Report(w.ctx, "coder", bus.ReportNote, "hand-off")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DeclareHandoff(w.ctx, "coder", rep, bus.AgentIdent{Pane: "p-coder", PID: testPID, PIDStart: testStart, Session: cs, Client: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	focused = false
	w.pass(92)
	if w.compacts() != 0 {
		t.Fatal("compacted over a standing hand-off")
	}
	if err := w.r.HDel(w.ctx, bus.HandoffKey(w.b.Project()), "coder").Err(); err != nil {
		t.Fatal(err)
	}
	w.pass(92)
	if w.compacts() != 1 {
		t.Fatal("did not compact once the guards cleared")
	}
}

// A /compact left in the input: the person is told, the reservation stays,
// nothing is typed again in the episode and master gets no notice.
func TestAutoCompactNotLanded(t *testing.T) {
	w := newCompactWorld(t, alwaysReady)
	w.rt.compactErr = termhost.ErrNotLanded
	w.pass(92)
	w.ageHold(11 * time.Minute)
	w.pass(92)
	w.pass(92)
	if w.compacts() != 1 {
		t.Fatalf("%d /compact typed, want 1", w.compacts())
	}
	if w.personNotices("/compact may be left unsent in coder's input") != 1 {
		t.Fatal("the person was not told")
	}
	if w.count("master", "auto-compact", false) != 0 {
		t.Fatal("master told of a /compact that did not land")
	}
}
