package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// noticeDeps are autoDeps whose notices are kept, in order; notifyErr, when
// set, is what every send returns (a broker down).
func (f *flow) noticeDeps(judge func(string, int) (Judged, error), notifyErr error) (Deps, *[]Notice) {
	d := f.autoDeps(judge)
	var ns []Notice
	d.Notify = func(_ context.Context, n Notice) error {
		ns = append(ns, n)
		return notifyErr
	}
	return d, &ns
}

func (f *flow) told(t *testing.T) map[string]string {
	t.Helper()
	return ReadAuto(Dir(f.env["XDG_STATE_HOME"])).Told
}

// §4.6: an install is told without a wake to every project restarted: from,
// to, the services restarted, the skills changed with their skill-users
// command, the undo; once per snapshot.
func TestNoticeInstalled(t *testing.T) {
	f := newFlow(t)
	d, ns := f.noticeDeps(quiet, nil)
	if code := InstallPass(context.Background(), d); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.errw.String())
	}
	if len(*ns) != 1 {
		t.Fatalf("notices %+v", *ns)
	}
	n := (*ns)[0]
	id := Snapshots(d.updates())[0]
	if n.Kind != NoticeInstalled || n.Wake || n.Key != id || !reflect.DeepEqual(n.Projects, []string{"a", "b"}) {
		t.Fatalf("notice %+v", n)
	}
	for _, want := range []string{"agentbus v0.1.0 -> v0.2.0 installed automatically; restarted: a dispatch, a missions, b dispatch.",
		"Skills changed: review (agents to tell: agentbus roles skill-users review).", "Undo: agentbus update --rollback " + id} {
		if !strings.Contains(n.Text, want) {
			t.Errorf("%q lacks %q", n.Text, want)
		}
	}
	if got := f.told(t)[NoticeInstalled]; got != id {
		t.Fatalf("told %q", got)
	}
}

// A project in work at its re-read keeps its old services: the notice goes
// to the projects restarted and to those left on the old build (master's
// 4d directive), and lists what was left with its command.
func TestNoticeInstalledNamesWhatWasLeft(t *testing.T) {
	f := newFlow(t)
	d, ns := f.noticeDeps(func(p string, call int) (Judged, error) {
		if p == "b" && call > 1 {
			return working(Judged{}), nil
		}
		return Judged{Now: autoNow}, nil
	}, nil)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 || !reflect.DeepEqual((*ns)[0].Projects, []string{"a", "b"}) ||
		!strings.Contains((*ns)[0].Text, "Not restarted: b dispatch (old agentbus), project b in work (agent coder is working): agentbus --project b service restart dispatch") {
		t.Fatalf("notices %+v", *ns)
	}
}

// Every project in work at its re-read: nothing restarted, and the notice
// still goes, to the projects left on the old build.
func TestNoticeInstalledNothingRestarted(t *testing.T) {
	f := newFlow(t)
	d, ns := f.noticeDeps(func(p string, call int) (Judged, error) {
		if call > 1 {
			return working(Judged{}), nil
		}
		return Judged{Now: autoNow}, nil
	}, nil)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 || (*ns)[0].Kind != NoticeInstalled || !reflect.DeepEqual((*ns)[0].Projects, []string{"a", "b"}) ||
		!strings.Contains((*ns)[0].Text, "Not restarted: a dispatch") {
		t.Fatalf("notices %+v", *ns)
	}
}

// A rollback is told with a wake to every project judged, once per tag: the
// next pass skips the tag and tells nothing more. A broker down loses the
// notice and keeps the history line.
func TestNoticeRolledBackOnce(t *testing.T) {
	f := newFlow(t)
	f.r.neverOK = "b dispatch"
	d, ns := f.noticeDeps(quiet, errors.New("broker down"))
	InstallPass(context.Background(), d)
	if len(*ns) != 1 {
		t.Fatalf("notices %+v", *ns)
	}
	n := (*ns)[0]
	if n.Kind != NoticeFailed || !n.Wake || n.Key != "v0.2.0" || !reflect.DeepEqual(n.Projects, []string{"a", "b"}) ||
		!strings.Contains(n.Text, "v0.1.0 -> v0.2.0 failed and was rolled back to v0.1.0: ") ||
		!strings.Contains(n.Text, "v0.2.0 is not installed automatically again") {
		t.Fatalf("notice %+v", n)
	}
	if !strings.Contains(f.errw.String(), "master's notice (failed) could not be sent: broker down") {
		t.Fatalf("the lost notice is not said:\n%s", f.errw.String())
	}
	if h := History(d.updates()); len(h) != 1 || h[0].OK {
		t.Fatalf("history %+v", h)
	}
	InstallPass(context.Background(), d) // skipped: not retried, not told again
	if len(*ns) != 1 || f.lastPass(t) != "skipped" {
		t.Fatalf("second pass: %s, notices %+v", f.lastPass(t), *ns)
	}
}

// The rollback failed too: the manual commands, with a wake.
func TestNoticeRollbackFailed(t *testing.T) {
	f := newFlow(t)
	updates := Dir(f.env["XDG_STATE_HOME"])
	f.r.neverOK = "a missions"
	f.r.onRestart = func() {
		for _, id := range Snapshots(updates) {
			_ = os.Remove(filepath.Join(updates, id, "bin", "busmon"))
		}
	}
	d, ns := f.noticeDeps(quiet, nil)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 {
		t.Fatalf("notices %+v\n%s", *ns, f.errw.String())
	}
	n := (*ns)[0]
	if n.Kind != NoticeRollbackFailed || !n.Wake || !strings.Contains(n.Text, "its rollback failed too. The person rolls back by hand:\n") {
		t.Fatalf("notice %+v", n)
	}
	for _, c := range manualCommands(filepath.Join(updates, n.Key), mustManifest(t, filepath.Join(updates, n.Key))) {
		if !strings.Contains(n.Text, c) {
			t.Errorf("%q lacks the command %q", n.Text, c)
		}
	}
}

func mustManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// An interrupted update is told once with a wake, naming its rollback.
func TestNoticeInterrupted(t *testing.T) {
	f := newFlow(t)
	updates := Dir(f.env["XDG_STATE_HOME"])
	if err := WriteSnapshot(filepath.Join(updates, "k1"), Manifest{BinDir: f.bin, From: "v0.1.0", To: "v0.2.0"}); err != nil {
		t.Fatal(err)
	}
	d, ns := f.noticeDeps(quiet, nil)
	InstallPass(context.Background(), d)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 {
		t.Fatalf("notices %+v", *ns)
	}
	n := (*ns)[0]
	if n.Kind != NoticeInterrupted || !n.Wake || n.Key != "k1" || !reflect.DeepEqual(n.Projects, []string{"a", "b"}) ||
		!strings.Contains(n.Text, "agentbus update --rollback k1") {
		t.Fatalf("notice %+v", n)
	}
}

// migrateFlow is a flow whose newest release, v0.3.0, declares a migration.
func migrateFlow(t *testing.T) *flow {
	f := newFlow(t)
	writeFile(t, filepath.Join(f.up, "docs", "releases", "v0.3.0.migrate"), "broker\n", 0o644)
	sh(t, f.up, "add", ".")
	sh(t, f.up, "commit", "-qm", "three")
	sh(t, f.up, "tag", "v0.3.0")
	return f
}

// A release that needs a migration is told once, without a wake; from the
// check pass too, but only when passes are automatic.
func TestNoticeMigrate(t *testing.T) {
	f := migrateFlow(t)
	d, ns := f.noticeDeps(quiet, nil)
	InstallPass(context.Background(), d)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 {
		t.Fatalf("notices %+v\n%s", *ns, f.out.String())
	}
	if n := (*ns)[0]; n.Kind != NoticeMigrate || n.Wake || n.Key != "v0.3.0" || !strings.Contains(n.Text, "v0.3.0 needs a broker migration") {
		t.Fatalf("notice %+v", n)
	}

	g := migrateFlow(t)
	d, ns = g.noticeDeps(quiet, nil)
	if CheckPass(context.Background(), d); len(*ns) != 1 || (*ns)[0].Kind != NoticeMigrate {
		t.Fatalf("check pass with install: %+v", *ns)
	}
	h := migrateFlow(t)
	d, ns = h.noticeDeps(quiet, nil)
	d.AutoSetting = func() string { return "notify" }
	if CheckPass(context.Background(), d); len(*ns) != 0 {
		t.Fatalf("check pass with notify told %+v", *ns)
	}
}

// waitSince writes a release waiting since the given time, and a last pass.
func (f *flow) waitSince(t *testing.T, since time.Time, last *AutoPass) {
	t.Helper()
	if err := WriteAuto(Dir(f.env["XDG_STATE_HOME"]), AutoState{Waiting: &AutoWaiting{Tag: "v0.2.0", Since: since}, LastPass: last}); err != nil {
		t.Fatal(err)
	}
}

// A release waiting longer than updates.max_wait is told once, with a wake,
// with the reason of the last pass: the project and agent in work; within
// max_wait, or with it off, nothing.
func TestNoticeWaitingInWork(t *testing.T) {
	busy := func(string, int) (Judged, error) { return working(Judged{}), nil }
	f := newFlow(t)
	f.waitSince(t, autoNow.Add(-8*24*time.Hour), nil)
	d, ns := f.noticeDeps(busy, nil)
	d.MaxWait = func() time.Duration { return 7 * 24 * time.Hour }
	InstallPass(context.Background(), d)
	InstallPass(context.Background(), d)
	if len(*ns) != 1 {
		t.Fatalf("notices %+v", *ns)
	}
	n := (*ns)[0]
	if n.Kind != NoticeWaiting || !n.Wake || n.Key != "v0.2.0" || !reflect.DeepEqual(n.Projects, []string{"a", "b"}) ||
		!strings.Contains(n.Text, "longer than updates.max_wait (168h0m0s)") ||
		!strings.Contains(n.Text, "was deferred: project a in work (agent coder is working); project b in work (agent coder is working)") {
		t.Fatalf("notice %+v", n)
	}

	for name, maxWait := range map[string]func() time.Duration{
		"within": func() time.Duration { return 9 * 24 * time.Hour }, "off": func() time.Duration { return 0 }, "unset": nil,
		"exactly max_wait is not longer": func() time.Duration { return 8 * 24 * time.Hour }} {
		g := newFlow(t)
		g.waitSince(t, autoNow.Add(-8*24*time.Hour), nil)
		d, ns := g.noticeDeps(busy, nil)
		d.MaxWait = maxWait
		InstallPass(context.Background(), d)
		if len(*ns) != 0 {
			t.Errorf("%s: told %+v", name, *ns)
		}
	}
}

// The check pass (outside the window) counts max_wait too: with no
// automatic pass since the release was found, the window was never reached.
func TestNoticeWaitingWindowNeverReached(t *testing.T) {
	f := newFlow(t)
	f.waitSince(t, autoNow.Add(-8*24*time.Hour), &AutoPass{At: autoNow.Add(-9 * 24 * time.Hour), Result: "current"})
	d, ns := f.noticeDeps(quiet, nil)
	d.MaxWait = func() time.Duration { return 7 * 24 * time.Hour }
	CheckPass(context.Background(), d)
	if len(*ns) != 1 || (*ns)[0].Kind != NoticeWaiting || !strings.Contains((*ns)[0].Text, "the window (updates.window) was never reached") {
		t.Fatalf("notices %+v", *ns)
	}
	if got := f.told(t)[NoticeWaiting]; got != "v0.2.0" {
		t.Fatalf("told %q", got)
	}
	g := newFlow(t)
	g.waitSince(t, autoNow.Add(-8*24*time.Hour), nil)
	d, ns = g.noticeDeps(quiet, nil)
	d.MaxWait = func() time.Duration { return 7 * 24 * time.Hour }
	d.AutoSetting = func() string { return "notify" }
	if CheckPass(context.Background(), d); len(*ns) != 0 {
		t.Fatalf("notify told %+v", *ns)
	}
}

// No project on this host: nobody is told, and nothing is marked told.
func TestNoticeNoProject(t *testing.T) {
	f := newFlow(t)
	f.services = nil
	d, ns := f.noticeDeps(quiet, nil)
	if err := WriteSnapshot(filepath.Join(d.updates(), "k1"), Manifest{BinDir: f.bin, From: "v0.1.0", To: "v0.2.0"}); err != nil {
		t.Fatal(err)
	}
	InstallPass(context.Background(), d)
	if len(*ns) != 0 || f.told(t)[NoticeInterrupted] != "" {
		t.Fatalf("told %+v, marks %v", *ns, f.told(t))
	}
}
