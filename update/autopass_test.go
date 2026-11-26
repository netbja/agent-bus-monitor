package update

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// autoDeps: the yes given ahead (updates.auto = install), no holder, and
// a judge whose answer per project and per call the test sets.
func (f *flow) autoDeps(judge func(project string, call int) (Judged, error)) Deps {
	d := f.deps(false)
	d.Holders = func(context.Context) ([]string, error) { return nil, nil }
	calls := map[string]int{}
	d.Judge = func(_ context.Context, p string) (Judged, error) {
		calls[p]++
		return judge(p, calls[p])
	}
	d.AutoSetting = func() string { return "install" }
	f.r.now = autoNow
	return d
}

func quiet(string, int) (Judged, error) { return Judged{Now: autoNow}, nil }

func working(Judged) Judged {
	return Judged{Now: autoNow, Agents: []AgentFact{{"coder", "working", autoNow}}}
}

func (f *flow) lastPass(t *testing.T) string {
	t.Helper()
	st := ReadAuto(Dir(f.env["XDG_STATE_HOME"]))
	if st.LastPass == nil {
		return ""
	}
	return st.LastPass.Result
}

// Every project quiet: the release is installed with no question, every
// project's services restarted, an "auto" history line, last_pass installed
// (quiet-update-auto §4.3 step 5).
func TestInstallPassInstalls(t *testing.T) {
	f := newFlow(t)
	if code := InstallPass(context.Background(), f.autoDeps(quiet)); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "new agentbus" {
		t.Fatal("not installed")
	}
	if strings.Join(f.r.restarted, ",") != "a dispatch,a missions,b dispatch" {
		t.Fatalf("restarted %v", f.r.restarted)
	}
	h := History(Dir(f.env["XDG_STATE_HOME"]))
	if len(h) != 1 || h[0].Kind != HistoryAuto || !h[0].OK || h[0].To != "v0.2.0" {
		t.Fatalf("history %+v", h)
	}
	if got := f.lastPass(t); got != "installed" {
		t.Fatalf("last_pass %q", got)
	}
	if st := ReadAuto(Dir(f.env["XDG_STATE_HOME"])); st.Waiting != nil {
		t.Fatalf("still waiting: %+v", st.Waiting)
	}
}

// The quiet test again, per project, right before its first restart: a
// project now in work keeps its old services, listed with their restart
// command, and nothing rolls back.
func TestInstallPassSkipsAProjectNowInWork(t *testing.T) {
	f := newFlow(t)
	d := f.autoDeps(func(p string, call int) (Judged, error) {
		if p == "b" && call > 1 {
			return working(Judged{}), nil
		}
		return Judged{Now: autoNow}, nil
	})
	if code := InstallPass(context.Background(), d); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	if strings.Join(f.r.restarted, ",") != "a dispatch,a missions" {
		t.Fatalf("restarted %v", f.r.restarted)
	}
	if !strings.Contains(f.out.String(), "not restarted: b dispatch (old agentbus), project b in work (agent coder is working): agentbus --project b service restart dispatch") {
		t.Fatalf("out:\n%s", f.out.String())
	}
	if h := History(Dir(f.env["XDG_STATE_HOME"])); len(h) != 1 || !h[0].OK {
		t.Fatalf("history %+v", h)
	}
	if got := f.lastPass(t); got != "installed" {
		t.Fatalf("last_pass %q", got)
	}
}

// A project in work, or one that cannot be read, defers the pass: nothing
// staged or installed, no history line.
func TestInstallPassDefers(t *testing.T) {
	for name, c := range map[string]struct {
		judge func(string, int) (Judged, error)
		code  int
		say   string
	}{
		"in work":    {func(p string, _ int) (Judged, error) { return working(Judged{}), nil }, 0, "project a: in work (agent coder is working): deferred"},
		"unreadable": {func(string, int) (Judged, error) { return Judged{}, errors.New("broker down") }, 1, "deferred"},
	} {
		f := newFlow(t)
		code := InstallPass(context.Background(), f.autoDeps(c.judge))
		if code != c.code || !strings.Contains(f.out.String()+f.errw.String(), c.say) {
			t.Errorf("%s: exit %d\n%s\n%s", name, code, f.out.String(), f.errw.String())
		}
		if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || len(f.r.restarted) != 0 || len(History(Dir(f.env["XDG_STATE_HOME"]))) != 0 {
			t.Errorf("%s: something changed (restarted %v)", name, f.r.restarted)
		}
		if got := f.lastPass(t); got != "deferred" {
			t.Errorf("%s: last_pass %q", name, got)
		}
	}
}

// A red service rolls the whole update back; the failed automatic install
// is recorded and never retried: the next pass skips the tag.
func TestInstallPassRollsBackAndNeverRetries(t *testing.T) {
	f := newFlow(t)
	f.r.neverOK = "b dispatch"
	d := f.autoDeps(quiet)
	if code := InstallPass(context.Background(), d); code != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	f.assertRolledBack(t)
	h := History(Dir(f.env["XDG_STATE_HOME"]))
	if len(h) != 1 || h[0].Kind != HistoryAuto || h[0].OK {
		t.Fatalf("history %+v", h)
	}
	if got := f.lastPass(t); got != "rolled_back" {
		t.Fatalf("last_pass %q", got)
	}
	f.out.Reset()
	f.r.restarted = nil
	if code := InstallPass(context.Background(), d); code != 0 || !strings.Contains(f.out.String(), "it is not retried") || len(f.r.restarted) != 0 {
		t.Fatalf("second pass: exit %d %v\n%s", code, f.r.restarted, f.out.String())
	}
	if got := f.lastPass(t); got != "skipped" {
		t.Fatalf("last_pass %q", got)
	}
}

// Without the yes given ahead, --auto is --check: nothing installed.
func TestInstallPassWithoutInstallChecks(t *testing.T) {
	f := newFlow(t)
	d := f.autoDeps(quiet)
	d.AutoSetting = func() string { return "notify" }
	if code := InstallPass(context.Background(), d); code != 0 || !strings.Contains(f.out.String(), "updates.auto is not install") {
		t.Fatalf("exit %d\n%s", code, f.out.String())
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || len(f.r.restarted) != 0 {
		t.Fatal("installed without the yes")
	}
}

// A held lock defers the pass; an interrupted update changes nothing and
// says so.
func TestInstallPassGuards(t *testing.T) {
	f := newFlow(t)
	d := f.autoDeps(quiet)
	unlock, err := Lock(d.updates())
	if err != nil {
		t.Fatal(err)
	}
	if code := InstallPass(context.Background(), d); code != 0 || !strings.Contains(f.out.String(), "another agentbus update is running: deferred") {
		t.Fatalf("held: exit %d\n%s", code, f.out.String())
	}
	unlock()
	if got := f.lastPass(t); got != "deferred" {
		t.Fatalf("last_pass %q", got)
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" {
		t.Fatal("installed under a held lock")
	}
}

// Up to date: last_pass current, nothing waiting.
func TestInstallPassUpToDate(t *testing.T) {
	f := newFlow(t)
	d := f.autoDeps(quiet)
	d.Version = "v0.2.0"
	if code := InstallPass(context.Background(), d); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.errw.String())
	}
	if got := f.lastPass(t); got != "current" {
		t.Fatalf("last_pass %q", got)
	}
}

// A project that cannot be read at its re-read is in work: its services stay
// on the old build (it fails closed).
func TestInstallPassUnreadableAtRestartSkips(t *testing.T) {
	f := newFlow(t)
	d := f.autoDeps(func(p string, call int) (Judged, error) {
		if p == "a" && call > 1 {
			return Judged{}, errors.New("broker down")
		}
		return Judged{Now: autoNow}, nil
	})
	if code := InstallPass(context.Background(), d); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	if strings.Join(f.r.restarted, ",") != "b dispatch" || !strings.Contains(f.out.String(), "project a in work (cannot be read: broker down)") {
		t.Fatalf("restarted %v\n%s", f.r.restarted, f.out.String())
	}
}
