package mergequeue

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// conflict-owner §4 and §6 "Track and send", on the disposable broker:
// RunConflicts decides, the fenced scripts write.

const (
	cfBase  = "dev"
	cfHead1 = "1111111111111111111111111111111111111111"
	cfHead2 = "2222222222222222222222222222222222222222"
	cfHead3 = "3333333333333333333333333333333333333333"
	cfHead4 = "4444444444444444444444444444444444444444"
	cfTip1  = "9999999999999999999999999999999999999991"
	cfTip2  = "9999999999999999999999999999999999999992"
)

type cfWorld struct {
	*ciWorld
	after time.Duration
	now   time.Time
}

func newCFWorld(t *testing.T) *cfWorld {
	t.Helper()
	w := &cfWorld{ciWorld: newCIWorld(t), after: 2 * time.Hour}
	now, err := w.b.ServerTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w.now = now
	w.state("coder", "idle") // a known agent: coder/<task> is coder's
	return w
}

func conflictOn(n int, branch, head, tip string, files ...string) bus.MergeConflict {
	return bus.MergeConflict{Number: n, Base: cfBase, Branch: branch, Head: head, BaseTip: tip, Files: files}
}

// pass runs one pass with these conflicts, every one open.
func (w *cfWorld) pass(cs ...bus.MergeConflict) string {
	w.t.Helper()
	open := map[string]bool{}
	for _, c := range cs {
		open[ConflictField(c.Number, c.Branch)] = true
	}
	return w.passWith(ConflictPass{Base: cfBase, Now: w.now, Conflicting: cs, Open: open})
}

func (w *cfWorld) passWith(p ConflictPass) string {
	w.t.Helper()
	why, err := RunConflicts(context.Background(), w.b, "tok", ConflictConfig{Master: "master", Cap: w.cap, EscalateAfter: w.after}, p)
	if err != nil {
		w.t.Fatal(err)
	}
	return why
}

func (w *cfWorld) entry(field string) (bus.ConflictEntry, bool) {
	w.t.Helper()
	rec, err := w.b.ConflictRecord(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	e, ok := rec[field]
	return e, ok
}

// age makes field's first older by d.
func (w *cfWorld) age(field string, d time.Duration) {
	w.t.Helper()
	e, ok := w.entry(field)
	if !ok {
		w.t.Fatalf("no field %s", field)
	}
	esc := "0"
	if e.Escalated {
		esc = "1"
	}
	w.r.HSet(context.Background(), bus.ConflictsKey(w.b.Project()), field,
		e.Last+"|"+strconv.Itoa(e.Count)+"|"+strconv.FormatInt(e.First-int64(d/time.Second), 10)+"|"+esc)
}

const pr7 = "pr:7"

// One hand-off per episode, to the owner, woken, with master's copy (wake=no);
// the same episode again sends nothing.
func TestConflictHandoffOncePerEpisode(t *testing.T) {
	w := newCFWorld(t)
	c := conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go", "b.go")
	if why := w.pass(c); why != "" {
		t.Fatalf("why %q", why)
	}
	cmds := w.cmds()
	if len(cmds) != 2 || cmds[0].target != "coder" || cmds[0].wake == "no" || cmds[1].target != "master" || cmds[1].wake != "no" {
		t.Fatalf("sent %+v", cmds)
	}
	for _, want := range []string{"PR #7 (coder/fix) at 1111111", "conflicts with dev at 9999999", "a.go, b.go", "Rebase it onto origin/dev", "Hand-off 1 of 3"} {
		if !strings.Contains(cmds[0].text, want) {
			t.Fatalf("no %q in %q", want, cmds[0].text)
		}
	}
	w.pass(c)
	if n := len(w.cmds()); n != 2 {
		t.Fatalf("the same episode sent again: %d", n)
	}
	if e, _ := w.entry(pr7); e.Count != 1 {
		t.Fatalf("count %d", e.Count)
	}
}

// A crash after the track and before the send: the next pass sends once,
// and the count does not rise twice.
func TestConflictCrashAfterTrack(t *testing.T) {
	w := newCFWorld(t)
	c := conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")
	if _, err := w.b.FencedConflictTrack(context.Background(), "tok", bus.ConflictTrack{Field: pr7, Digest: ConflictDigest(c), Count: true}); err != nil {
		t.Fatal(err)
	}
	w.pass(c)
	w.pass(c)
	if cmds := w.cmds(); len(cmds) != 2 || !strings.Contains(cmds[0].text, "Hand-off 1 of 3") {
		t.Fatalf("sent %+v", cmds)
	}
	if e, _ := w.entry(pr7); e.Count != 1 {
		t.Fatalf("count %d", e.Count)
	}
}

// A new head that still conflicts is a new episode; a base move under the
// same head sends nothing and counts nothing.
func TestConflictNewHeadCountsBaseMoveDoesNot(t *testing.T) {
	w := newCFWorld(t)
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go"))
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip2, "a.go", "c.go"))
	if n := len(w.cmds()); n != 2 {
		t.Fatalf("a base move sent: %d", n)
	}
	w.cap = 3
	for i := 0; i < 10; i++ { // ten base moves under one head: no escalation by the cap
		w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip2, "a.go"))
	}
	if e, _ := w.entry(pr7); e.Count != 1 || e.Escalated {
		t.Fatalf("after base moves: %+v", e)
	}
	w.pass(conflictOn(7, "coder/fix", cfHead2, cfTip2, "a.go"))
	cmds := w.cmds()
	if len(cmds) != 4 || !strings.Contains(cmds[2].text, "Hand-off 2 of 3") {
		t.Fatalf("new head: %+v", cmds)
	}
}

// With a cap of 3: three hand-offs, the fourth episode escalates to master,
// woken; nothing more to the owner after it. A cap of 0 escalates the first.
func TestConflictCap(t *testing.T) {
	w := newCFWorld(t)
	for _, h := range []string{cfHead1, cfHead2, cfHead3, cfHead4} {
		w.pass(conflictOn(7, "coder/fix", h, cfTip1, "a.go"))
	}
	cmds := w.cmds()
	if len(cmds) != 7 {
		t.Fatalf("sent %d: %+v", len(cmds), cmds)
	}
	esc := cmds[6]
	if esc.target != "master" || esc.wake == "no" || !strings.Contains(esc.text, "still conflicts with dev after 3 hand-off(s)") || !strings.Contains(esc.text, "owner coder") {
		t.Fatalf("escalation %+v", esc)
	}
	w.pass(conflictOn(7, "coder/fix", "5555555555555555555555555555555555555555", cfTip1, "a.go"))
	if n := len(w.cmds()); n != 7 {
		t.Fatalf("sent after the escalation: %d", n)
	}

	w0 := newCFWorld(t)
	w0.cap = 0
	w0.pass(conflictOn(8, "coder/other", cfHead1, cfTip1, "a.go"))
	if cmds := w0.cmds(); len(cmds) != 1 || cmds[0].target != "master" || !strings.Contains(cmds[0].text, "after 0 hand-off(s)") {
		t.Fatalf("cap 0: %+v", cmds)
	}
}

// The age bound: past conflict.escalate_after, master has it, even below the
// cap; a new head then sends nothing to the owner. 0: no time bound.
func TestConflictAge(t *testing.T) {
	w := newCFWorld(t)
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go"))
	w.age(pr7, 3*time.Hour)
	w.after = 0
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go"))
	if n := len(w.cmds()); n != 2 {
		t.Fatalf("escalate_after 0 escalated: %d", n)
	}
	w.after = 2 * time.Hour
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go"))
	cmds := w.cmds()
	if len(cmds) != 3 || cmds[2].target != "master" || !strings.Contains(cmds[2].text, "after 1 hand-off(s) / 3h0m") {
		t.Fatalf("age: %+v", cmds)
	}
	w.pass(conflictOn(7, "coder/fix", cfHead2, cfTip1, "a.go"))
	if n := len(w.cmds()); n != 3 {
		t.Fatalf("a new head after the escalation sent: %d", n)
	}
}

// A crash between escalate and its send: the next pass sends it, once.
func TestConflictCrashAfterEscalate(t *testing.T) {
	w := newCFWorld(t)
	c := conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")
	if _, err := w.b.FencedConflictTrack(context.Background(), "tok", bus.ConflictTrack{Field: pr7, Digest: ConflictDigest(c), Escalate: true}); err != nil {
		t.Fatal(err)
	}
	w.pass(c)
	w.pass(c)
	if cmds := w.cmds(); len(cmds) != 1 || cmds[0].target != "master" || !strings.Contains(cmds[0].text, "still conflicts") {
		t.Fatalf("sent %+v", cmds)
	}
}

// A held owner: nothing sent, not counted, but first is set and the age escalates.
func TestConflictHeldOwner(t *testing.T) {
	w := newCFWorld(t)
	w.state("coder", "blocked")
	c := conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")
	w.pass(c)
	e, ok := w.entry(pr7)
	if !ok || e.Count != 0 || e.First == 0 || len(w.cmds()) != 0 {
		t.Fatalf("held: %+v %v %+v", e, ok, w.cmds())
	}
	w.age(pr7, 3*time.Hour)
	w.pass(c)
	if cmds := w.cmds(); len(cmds) != 1 || cmds[0].target != "master" || !strings.Contains(cmds[0].text, "after 0 hand-off(s)") {
		t.Fatalf("held past the age: %+v", cmds)
	}
}

// Resolved: a clean approved head, or a PR no longer open: the field and its
// marks go; a later conflict starts afresh. Open but not a candidate (a fix
// in review): the field is kept, untouched, and the next approved head that
// still conflicts is counted from where it was.
func TestConflictResolvedAndPush(t *testing.T) {
	w := newCFWorld(t)
	w.pass(conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go"))
	// A push: the new head is open but in review, past the age bound too.
	w.age(pr7, 3*time.Hour)
	w.passWith(ConflictPass{Base: cfBase, Now: w.now, Open: map[string]bool{pr7: true}})
	if e, ok := w.entry(pr7); !ok || e.Count != 1 || e.Escalated || len(w.cmds()) != 2 {
		t.Fatalf("in review: %+v %v %d", e, ok, len(w.cmds()))
	}
	// Approved and clean: resolved, the marks with it.
	w.passWith(ConflictPass{Base: cfBase, Now: w.now, Open: map[string]bool{pr7: true}, Clean: []string{pr7}})
	if _, ok := w.entry(pr7); ok {
		t.Fatal("a clean head kept the field")
	}
	q, _ := w.r.HGetAll(context.Background(), bus.QuotaKey(w.b.Project())).Result()
	for _, m := range bus.ConflictMarks(pr7) {
		if _, ok := q[m]; ok {
			t.Fatalf("mark %s kept", m)
		}
	}
	// A later conflict starts afresh: hand-off 1 again.
	w.pass(conflictOn(7, "coder/fix", cfHead2, cfTip1, "a.go"))
	if cmds := w.cmds(); len(cmds) != 4 || !strings.Contains(cmds[2].text, "Hand-off 1 of 3") {
		t.Fatalf("afresh: %+v", cmds)
	}
	// Closed: not open any more.
	w.passWith(ConflictPass{Base: cfBase, Now: w.now, Open: map[string]bool{}})
	if _, ok := w.entry(pr7); ok {
		t.Fatal("a closed PR kept the field")
	}
}

// Three failed fixes reach the cap: each approved head still conflicting is
// counted, with reviews in between.
func TestConflictThreeFixesReachTheCap(t *testing.T) {
	w := newCFWorld(t)
	inReview := ConflictPass{Base: cfBase, Now: w.now, Open: map[string]bool{pr7: true}}
	for _, h := range []string{cfHead1, cfHead2, cfHead3, cfHead4} {
		w.pass(conflictOn(7, "coder/fix", h, cfTip1, "a.go"))
		w.passWith(inReview)
	}
	if e, _ := w.entry(pr7); e.Count != 3 || !e.Escalated {
		t.Fatalf("entry %+v", e)
	}
}

// The owner: the board's, else a known <role>/ prefix, else master, who then
// gets the hand-off and no copy. A branch in the git fallback has no number.
func TestConflictOwnerAndGitFallback(t *testing.T) {
	w := newCFWorld(t)
	w.pass(conflictOn(0, "feat/x", cfHead1, cfTip1, "a.go"))
	cmds := w.cmds()
	if len(cmds) != 1 || cmds[0].target != "master" || cmds[0].wake == "no" || !strings.Contains(cmds[0].text, "Branch feat/x at 1111111") {
		t.Fatalf("master's own: %+v", cmds)
	}
	if _, ok := w.entry(ConflictField(0, "feat/x")); !ok {
		t.Fatal("no field for the branch")
	}
	if ConflictField(0, "feat/x") == ConflictField(0, "feat/y") || !strings.HasPrefix(ConflictField(0, "feat/x"), "br:") || ConflictField(7, "x") != pr7 {
		t.Fatal("fields do not name their PR or branch")
	}
}

// A conflict git did not confirm (the forge's flag alone) is not handed off.
func TestConflictUnconfirmedIgnored(t *testing.T) {
	w := newCFWorld(t)
	w.pass(bus.MergeConflict{Number: 7, Base: cfBase, Branch: "coder/fix", Head: cfHead1})
	if len(w.cmds()) != 0 {
		t.Fatalf("sent %+v", w.cmds())
	}
	if _, ok := w.entry(pr7); ok {
		t.Fatal("tracked")
	}
}

// A swap in progress defers the send; the next pass sends it.
func TestConflictSwapDefers(t *testing.T) {
	w := newCFWorld(t)
	ctx := context.Background()
	w.r.Set(ctx, bus.SwapKey(w.b.Project(), "coder"), "1", time.Minute)
	c := conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")
	if why := w.pass(c); why != "" {
		t.Fatalf("why %q", why)
	}
	if cmds := w.cmds(); len(cmds) != 0 {
		t.Fatalf("sent during the swap: %+v", cmds)
	}
	w.r.Del(ctx, bus.SwapKey(w.b.Project(), "coder"))
	w.pass(c)
	if cmds := w.cmds(); len(cmds) != 2 || !strings.Contains(cmds[0].text, "Hand-off 1 of 3") {
		t.Fatalf("after the swap: %+v", cmds)
	}
}

// Not the lease holder: nothing written, and the error says so.
func TestConflictNotHolder(t *testing.T) {
	w := newCFWorld(t)
	_, err := RunConflicts(context.Background(), w.b, "other", ConflictConfig{Master: "master", Cap: 3, EscalateAfter: time.Hour},
		ConflictPass{Base: cfBase, Now: w.now, Conflicting: []bus.MergeConflict{conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")}, Open: map[string]bool{pr7: true}})
	if !errors.Is(err, bus.ErrNotHolder) || len(w.cmds()) != 0 {
		t.Fatalf("err %v sent %d", err, len(w.cmds()))
	}
	if _, ok := w.entry(pr7); ok {
		t.Fatal("tracked without the lease")
	}
}

// The budget: candidates beyond it are left for the next pass, named.
func TestConflictBudget(t *testing.T) {
	w := newCFWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), conflictReserve/2) // the reads fit, no candidate does
	defer cancel()
	why, err := RunConflicts(ctx, w.b, "tok", ConflictConfig{Master: "master", Cap: 3, EscalateAfter: time.Hour},
		ConflictPass{Base: cfBase, Now: w.now, Conflicting: []bus.MergeConflict{conflictOn(7, "coder/fix", cfHead1, cfTip1, "a.go")}, Open: map[string]bool{pr7: true}})
	if err != nil || !strings.Contains(why, "1 left for the next pass") {
		t.Fatalf("why %q err %v", why, err)
	}
}
