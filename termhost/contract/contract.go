// Package contract is the contract suite every termhost.Host adapter passes
// (spec 2026-11-10-herdr-exit-design.md, 4.2 and part 7 step 1): the fake
// today, the holder's adapter in a later step, each through a Harness that
// scripts the program in the terminal.
package contract

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/termhost"
)

// Harness is a Host under test and the controls of the scripted program
// running in each of its terminals. An adapter's harness runs a program it
// can script (the holder's: a test program on a real PTY).
type Harness interface {
	Host() termhost.Host
	// SetReady: the program shows its ready signature on a settled screen.
	SetReady(id termhost.TermID, ready bool)
	// SetInputEmpty: the program's input region holds nothing.
	SetInputEmpty(id termhost.TermID, empty bool)
	// SetLands: a pasted line shows in the program's input before CR.
	SetLands(id termhost.TermID, lands bool)
	// SetPersonActive: a viewer is typing, or a draft stands.
	SetPersonActive(id termhost.TermID, active bool)
	// Submitted is what the program received as submitted lines (a line
	// and its CR), in order.
	Submitted(id termhost.TermID) []string
}

// Run runs the contract against the harnesses newHarness makes, one per subtest.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	start := func(t *testing.T, h Harness, name string) termhost.Term {
		t.Helper()
		term, err := h.Host().Start(ctx, termhost.Spec{Project: "demo", Name: name, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
		return term
	}
	ready := func(h Harness, id termhost.TermID) {
		h.SetReady(id, true)
		h.SetInputEmpty(id, true)
		h.SetLands(id, true)
		h.SetPersonActive(id, false)
	}
	wake := func(t *testing.T, nonce string) termhost.Line {
		t.Helper()
		l, err := termhost.WakeLine("demo", "coder", 2, nonce)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	t.Run("StartListClose", func(t *testing.T) {
		h := newHarness(t)
		a := start(t, h, "coder")
		if a.ID == "" || !a.Alive || a.PID <= 0 || a.Project != "demo" || a.Name != "coder" {
			t.Fatalf("started %+v", a)
		}
		terms, err := h.Host().List(ctx, "demo")
		if err != nil || len(terms) != 1 || terms[0].ID != a.ID {
			t.Fatalf("List %+v %v", terms, err)
		}
		if other, _ := h.Host().List(ctx, "other"); len(other) != 0 {
			t.Fatalf("another project lists %+v", other)
		}
		if err := h.Host().Close(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		terms, _ = h.Host().List(ctx, "demo")
		if slices.ContainsFunc(terms, func(x termhost.Term) bool { return x.ID == a.ID && x.Alive }) {
			t.Fatalf("closed but alive: %+v", terms)
		}
	})
	t.Run("StartNeverReuses", func(t *testing.T) {
		h := newHarness(t)
		a, b := start(t, h, "coder"), start(t, h, "coder")
		if a.ID == b.ID {
			t.Fatalf("two starts, one terminal %s", a.ID)
		}
	})
	t.Run("StartRefusesAnInvalidName", func(t *testing.T) {
		h := newHarness(t)
		for _, s := range []termhost.Spec{{Project: "Demo", Name: "coder"}, {Project: "demo", Name: "co der"}, {Project: "demo"}} {
			if _, err := h.Host().Start(ctx, s); !errors.Is(err, termhost.ErrInvalidName) {
				t.Errorf("%+v: %v", s, err)
			}
		}
	})
	t.Run("UnknownTerm", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.Host().Screen(ctx, "nope", 10); !errors.Is(err, termhost.ErrUnknownTerm) {
			t.Errorf("Screen: %v", err)
		}
		if err := h.Host().Submit(ctx, "nope", termhost.ClearLine()); !errors.Is(err, termhost.ErrUnknownTerm) {
			t.Errorf("Submit: %v", err)
		}
	})
	t.Run("SubmitTypesTheLine", func(t *testing.T) {
		h := newHarness(t)
		a := start(t, h, "coder")
		ready(h, a.ID)
		l := wake(t, "abc123")
		if err := h.Host().Submit(ctx, a.ID, l); err != nil {
			t.Fatal(err)
		}
		// A real program redraws on its own time after the CR: until its input
		// reads empty again, a host refuses the next line (ErrNotReady). Wait
		// for the first line to show as submitted, as a caller would.
		for deadline := time.Now().Add(3 * time.Second); len(h.Submitted(a.ID)) == 0 && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		if err := h.Host().Submit(ctx, a.ID, termhost.ClearLine()); err != nil {
			t.Fatal(err)
		}
		if got := h.Submitted(a.ID); !slices.Equal(got, []string{l.Text(), "/clear"}) {
			t.Fatalf("submitted %q", got)
		}
		if seen, err := h.Host().WaitScreen(ctx, a.ID, l.Marker(), time.Second); err != nil || !seen {
			t.Fatalf("marker not on screen: %v %v", seen, err)
		}
	})
	t.Run("SubmitRefusesTheZeroLine", func(t *testing.T) {
		h := newHarness(t)
		a := start(t, h, "coder")
		ready(h, a.ID)
		if err := h.Host().Submit(ctx, a.ID, termhost.Line{}); !errors.Is(err, termhost.ErrInvalidLine) {
			t.Fatalf("zero line: %v", err)
		}
		if got := h.Submitted(a.ID); len(got) != 0 {
			t.Fatalf("typed %q", got)
		}
	})
	t.Run("SubmitWritesNothingWhenRefused", func(t *testing.T) {
		for name, c := range map[string]struct {
			set  func(Harness, termhost.TermID)
			want error
		}{
			"person active":   {func(h Harness, id termhost.TermID) { h.SetPersonActive(id, true) }, termhost.ErrPersonActive},
			"not ready":       {func(h Harness, id termhost.TermID) { h.SetReady(id, false) }, termhost.ErrNotReady},
			"input not empty": {func(h Harness, id termhost.TermID) { h.SetInputEmpty(id, false) }, termhost.ErrNotReady},
		} {
			h := newHarness(t)
			a := start(t, h, "coder")
			ready(h, a.ID)
			c.set(h, a.ID)
			if err := h.Host().Submit(ctx, a.ID, wake(t, "abc123")); !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", name, err, c.want)
			}
			if got := h.Submitted(a.ID); len(got) != 0 {
				t.Errorf("%s: typed %q", name, got)
			}
		}
	})
	t.Run("NotLandedLeavesBehind", func(t *testing.T) {
		h := newHarness(t)
		a := start(t, h, "coder")
		ready(h, a.ID)
		h.SetLands(a.ID, false)
		if err := h.Host().Submit(ctx, a.ID, termhost.ClearLine()); !errors.Is(err, termhost.ErrNotLanded) {
			t.Fatalf("not seen: %v", err)
		}
		if got := h.Submitted(a.ID); len(got) != 0 {
			t.Fatalf("a line not seen was submitted: %q", got)
		}
		// A left-behind /clear must never be completed by the next wake.
		h.SetLands(a.ID, true)
		if err := h.Host().Submit(ctx, a.ID, wake(t, "abc123")); !errors.Is(err, termhost.ErrLeftBehind) { // its own error: the dispatcher reports it
			t.Fatalf("submit after a left-behind line: %v", err)
		}
		if got := h.Submitted(a.ID); len(got) != 0 {
			t.Fatalf("typed after a left-behind line: %q", got)
		}
		// The mark clears once a settled screen shows the input empty: a host
		// that watches the screen lifts it on its own time, so the submit may
		// be refused as left behind until then, never longer than 5 s.
		h.SetInputEmpty(a.ID, true)
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := h.Host().Submit(ctx, a.ID, wake(t, "abc124"))
			if err == nil {
				break
			}
			if !errors.Is(err, termhost.ErrLeftBehind) || time.Now().After(deadline) {
				t.Fatalf("input empty again: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	t.Run("Ping", func(t *testing.T) {
		if err := newHarness(t).Host().Ping(ctx); err != nil {
			t.Fatal(err)
		}
	})
}
