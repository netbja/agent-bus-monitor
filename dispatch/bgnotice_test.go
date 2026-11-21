package dispatch

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// background-work-visible §4a: a skip that lasts background_notice_after
// sends one notice to the escalation target per episode (a restart sends no
// second); the episode ends when the action is no longer skipped, and a new
// one notices again; 0 sends none; the target's own skip is wake=no.
func TestBackgroundSkipNoticeOncePerEpisode(t *testing.T) {
	w := newClearWorld(t, true)
	w.l.cfg.BackgroundNoticeAfter = time.Millisecond
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	notices := func() []bus.Event {
		evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 50)
		if err != nil {
			t.Fatal(err)
		}
		var out []bus.Event
		for _, e := range evs {
			if strings.HasPrefix(e.Message, "background: coder's auto-clear has been skipped") {
				out = append(out, e)
			}
		}
		return out
	}
	w.l.cfg.BackgroundNoticeAfter = time.Hour // the threshold not reached: no notice (foureyes K3)
	w.pass()                                  // the episode opens
	marks, _ := w.b.QuotaMarks(context.Background())
	if marks[bus.BgSkipMark("coder", "auto-clear")] == "" {
		t.Fatalf("no episode mark: %v", marks)
	}
	w.pass()
	if n := notices(); len(n) != 0 {
		t.Fatalf("a notice before background_notice_after: %+v", n)
	}
	w.l.cfg.BackgroundNoticeAfter = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	w.pass()
	n := notices()
	if len(n) != 1 || n[0].Target != "master" || n[0].NoWake || !strings.Contains(n[0].Message, "process: process 'pytest'") {
		t.Fatalf("notices %+v", n)
	}
	w.pass()
	w.l = &loop{b: w.b, rt: w.rt, token: "clear-holder", last: map[string]string{}, // a dispatcher restart
		cfg: Config{EscalateTo: "master", Log: io.Discard, Clear: ClearConfig{On: true, Proof: 30 * time.Second}, BackgroundNoticeAfter: time.Millisecond}}
	w.pass()
	if n := notices(); len(n) != 1 {
		t.Fatalf("%d notices after a restart", len(n))
	}
	// The work ends: the /clear runs, the episode's marks go.
	bg = judged("none")
	w.pass()
	marks, _ = w.b.QuotaMarks(context.Background())
	if marks[bus.BgSkipMark("coder", "auto-clear")] != "" || marks[bus.BgSkipNoticeMark("coder", "auto-clear")] != "" {
		t.Fatalf("episode marks left: %v", marks)
	}
}

func TestBackgroundSkipNoticeOffAndSelf(t *testing.T) {
	w := newClearWorld(t, true)
	w.l.cfg.BackgroundNoticeAfter = 0 // off
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	w.pass()
	time.Sleep(5 * time.Millisecond)
	w.pass()
	evs, _, _ := w.b.Recent(context.Background(), []string{"cmd"}, 50)
	for _, e := range evs {
		if strings.HasPrefix(e.Message, "background: ") {
			t.Fatalf("a notice with the setting off: %+v", e)
		}
	}
	// The escalation target's own skip: told wake=no.
	w2 := newClearWorld(t, true)
	w2.l.cfg.BackgroundNoticeAfter, w2.l.cfg.EscalateTo = time.Millisecond, "coder"
	w2.rt.script = w.rt.script
	w2.pass()
	time.Sleep(5 * time.Millisecond)
	w2.pass()
	evs, _, _ = w2.b.Recent(context.Background(), []string{"cmd"}, 50)
	found := false
	for _, e := range evs {
		if strings.HasPrefix(e.Message, "background: coder's auto-clear") {
			found = true
			if !e.NoWake || e.Target != "coder" {
				t.Fatalf("self notice %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("no self notice")
	}
}

// A pop-retire held by background work has its own episode and notice.
func TestBackgroundSkipNoticePopRetire(t *testing.T) {
	w := newPopWorld(t, "pop")
	w.l.cfg.BackgroundNoticeAfter = time.Millisecond
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	w.pass()
	time.Sleep(5 * time.Millisecond)
	w.pass()
	var n []string
	for _, d := range w.directives() {
		if strings.Contains(d, "background: security's pop-retire has been skipped") {
			n = append(n, d)
		}
	}
	if len(n) != 1 || !strings.HasPrefix(n[0], "|") { // wake=yes: the field is absent
		t.Fatalf("pop-retire notices %q (all %q)", n, w.directives())
	}
}

// An action whose pass does not run skips nothing: turning pop-retire off, or
// pausing the project, ends the open episodes, so the time off never counts
// toward a notice (reviewer-codex).
func TestBackgroundSkipEpisodeEndsWhenThePassDoesNotRun(t *testing.T) {
	w := newPopWorld(t, "pop")
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	w.pass()
	marks, _ := w.b.QuotaMarks(context.Background())
	if marks[bus.BgSkipMark("security", "pop-retire")] == "" {
		t.Fatalf("no episode opened: %v", marks)
	}
	w.l.cfg.PopRetire.On = false
	w.pass()
	if marks, _ = w.b.QuotaMarks(context.Background()); marks[bus.BgSkipMark("security", "pop-retire")] != "" {
		t.Fatalf("the episode stayed open with pop-retire off: %v", marks)
	}
	// Paused: both actions' episodes end.
	w.l.cfg.PopRetire.On = true
	w.pass()
	if err := w.l.bgEndEpisodes(context.Background(), "auto-clear", "pop-retire"); err != nil {
		t.Fatal(err)
	}
	if marks, _ = w.b.QuotaMarks(context.Background()); marks[bus.BgSkipMark("security", "pop-retire")] != "" {
		t.Fatalf("an episode stayed open through the pause's end call: %v", marks)
	}
}

// A paused project's loop ends the open episodes (the pause path, end to end).
func TestBackgroundSkipEpisodeEndsWhilePaused(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "dev", "p1")
	r.HSet(ctx, bus.QuotaKey(b.Project()), bus.BgSkipMark("dev", "auto-clear"), "1:42", bus.BgSkipNoticeMark("dev", "auto-clear"), "1:42")
	if _, err := b.SetPause(ctx, "charlie", "master", "test", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	start(t, b, newFakeRT(alwaysReady), fastConfig())
	waitFor(t, 5*time.Second, "the episode's marks gone", func() bool {
		m, _ := b.QuotaMarks(ctx)
		return m[bus.BgSkipMark("dev", "auto-clear")] == "" && m[bus.BgSkipNoticeMark("dev", "auto-clear")] == ""
	})
}

// A new process is a new episode (foureyes K2): the start mark takes the new
// pid, the notice mark goes, and no notice fires until the threshold passes
// again.
func TestBackgroundSkipEpisodeNewProcess(t *testing.T) {
	w := newClearWorld(t, true)
	ctx := context.Background()
	w.l.cfg.BackgroundNoticeAfter = time.Millisecond
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	w.pass()
	time.Sleep(5 * time.Millisecond)
	w.pass() // noticed under the first process
	marks, _ := w.b.QuotaMarks(ctx)
	first := marks[bus.BgSkipMark("coder", "auto-clear")]
	if marks[bus.BgSkipNoticeMark("coder", "auto-clear")] != first || !strings.HasSuffix(first, ":"+strconv.Itoa(testPID)) {
		t.Fatalf("first episode %q, notice %q", first, marks[bus.BgSkipNoticeMark("coder", "auto-clear")])
	}
	// The same skip under another process: bgEpisodes as the pass calls it.
	w.l.bgPassStart("auto-clear")
	w.l.bgSkipped("auto-clear", "coder", bg, testPID+1)
	w.l.cfg.BackgroundNoticeAfter = time.Hour
	now, _ := w.b.ServerNowMS(ctx)
	if err := w.l.bgEpisodes(ctx, "auto-clear", marks, now); err != nil {
		t.Fatal(err)
	}
	marks, _ = w.b.QuotaMarks(ctx)
	if got := marks[bus.BgSkipMark("coder", "auto-clear")]; got == first || !strings.HasSuffix(got, ":"+strconv.Itoa(testPID+1)) {
		t.Fatalf("new process: start mark %q (was %q)", got, first)
	}
	if marks[bus.BgSkipNoticeMark("coder", "auto-clear")] != "" {
		t.Fatalf("the old episode's notice mark kept: %v", marks)
	}
}
