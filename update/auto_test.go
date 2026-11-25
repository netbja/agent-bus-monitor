package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

var autoNow = time.Date(2026, 11, 25, 3, 5, 0, 0, time.UTC)

// §4.3 step 4: working, or a status changed under 10 minutes ago, or a
// mission checking or integrating keeps a project in work; anything else is
// quiet (§8).
func TestInWork(t *testing.T) {
	ago := func(d time.Duration) time.Time { return autoNow.Add(-d) }
	for _, c := range []struct {
		name string
		j    Judged
		want string
	}{
		{"quiet", Judged{Agents: []AgentFact{{"coder", "idle", ago(time.Hour)}}, Missions: []MissionFact{{"c1", "m1", "in_review"}, {"c1", "m2", "integrated"}}}, ""},
		{"working", Judged{Agents: []AgentFact{{"coder", "working", ago(time.Hour)}}}, "agent coder is working"},
		{"9 minutes", Judged{Agents: []AgentFact{{"docs", "idle", ago(9 * time.Minute)}}}, "agent docs changed its status 9m0s ago"},
		{"11 minutes", Judged{Agents: []AgentFact{{"docs", "idle", ago(11 * time.Minute)}}}, ""},
		{"checking", Judged{Missions: []MissionFact{{"c1", "m1", "checking"}}}, "mission c1/m1 is checking"},
		{"integrating", Judged{Missions: []MissionFact{{"c1", "m1", "integrating"}}}, "mission c1/m1 is integrating"},
	} {
		c.j.Now = autoNow
		if got := InWork(c.j); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The projects judged: a running service's or an active holder's (with its
// dispatch and missions down); one with neither is not judged. Any read
// that fails fails the test closed.
func TestQuietJudgesServicesAndHolders(t *testing.T) {
	var judged []string
	d := Deps{
		Running: func(context.Context) ([]Service, error) {
			return []Service{{"a", "dispatch", "x"}, {"a", "missions", "x"}}, nil
		},
		Holders: func(context.Context) ([]string, error) { return []string{"h"}, nil },
		Judge: func(_ context.Context, p string) (Judged, error) {
			judged = append(judged, p)
			j := Judged{Now: autoNow}
			if p == "h" {
				j.Agents = []AgentFact{{"coder", "working", autoNow.Add(-time.Hour)}}
			}
			return j, nil
		},
	}
	got, err := Quiet(context.Background(), d)
	if err != nil || len(got) != 2 || got[0] != (ProjectWork{"a", ""}) || got[1] != (ProjectWork{"h", "agent coder is working"}) {
		t.Fatalf("quiet: %+v %v", got, err)
	}
	if sort.Strings(judged); strings.Join(judged, ",") != "a,h" {
		t.Fatalf("judged %v, want a (its services) and h (its holder), once each", judged)
	}
	boom := errors.New("boom")
	for name, mut := range map[string]func(*Deps){
		"services": func(d *Deps) { d.Running = func(context.Context) ([]Service, error) { return nil, boom } },
		"holders":  func(d *Deps) { d.Holders = func(context.Context) ([]string, error) { return nil, boom } },
		"broker":   func(d *Deps) { d.Judge = func(context.Context, string) (Judged, error) { return Judged{}, boom } },
		"unwired":  func(d *Deps) { d.Holders = nil },
	} {
		dd := d
		mut(&dd)
		if ps, err := Quiet(context.Background(), dd); err == nil {
			t.Errorf("%s unreadable: %+v, no error (it must fail closed)", name, ps)
		}
	}
}

// auto.json: written atomically and read back; missing or unreadable reads
// as empty; a waiting tag keeps its since until another tag replaces it.
func TestAutoState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "updates")
	if s := ReadAuto(dir); s.LastPass != nil || s.Waiting != nil || !s.LastCheck.IsZero() {
		t.Fatalf("missing: %+v", s)
	}
	s := AutoState{LastPass: &AutoPass{At: autoNow, Result: "deferred"}, LastCheck: autoNow, Told: map[string]string{"installed": "v0.2.0"}}
	s.waitingFor("v0.2.0", autoNow)
	s.waitingFor("v0.2.0", autoNow.Add(time.Hour))
	if err := WriteAuto(dir, s); err != nil {
		t.Fatal(err)
	}
	got := ReadAuto(dir)
	if got.LastPass == nil || got.LastPass.Result != "deferred" || !got.Waiting.Since.Equal(autoNow) || got.Told["installed"] != "v0.2.0" || !got.LastCheck.Equal(autoNow) {
		t.Fatalf("read back: %+v", got)
	}
	got.waitingFor("v0.3.0", autoNow.Add(2*time.Hour))
	if got.Waiting.Tag != "v0.3.0" || !got.Waiting.Since.Equal(autoNow.Add(2*time.Hour)) {
		t.Fatalf("a new tag: %+v", got.Waiting)
	}
	if err := os.WriteFile(filepath.Join(dir, "auto.json"), []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := ReadAuto(dir); s.LastPass != nil {
		t.Fatalf("garbage: %+v", s)
	}
}

// §4.3 step 3: a tag carrying its .migrate file and a tag whose last
// automatic install failed are skipped; a manual failure, or a later
// automatic success, does not skip it.
func TestSkip(t *testing.T) {
	w := newWorld(t)
	updates := t.TempDir()
	dir, err := GitDir(context.Background(), w.clone)
	if err != nil {
		t.Fatal(err)
	}
	rel := Release{Tag: "v0.2.0", GitDir: dir}
	if why := Skip(context.Background(), updates, rel); why != "" {
		t.Fatalf("a plain release skipped: %s", why)
	}
	for _, h := range []HistoryLine{{Kind: "update", To: "v0.2.0", OK: false}} {
		if err := AppendHistory(updates, h); err != nil {
			t.Fatal(err)
		}
	}
	if why := Skip(context.Background(), updates, rel); why != "" {
		t.Fatalf("a manual failure skipped it: %s", why)
	}
	AppendHistory(updates, HistoryLine{Kind: HistoryAuto, To: "v0.2.0", OK: false})
	if why := Skip(context.Background(), updates, rel); !strings.Contains(why, "failed: it is not retried") {
		t.Fatalf("a failed automatic install: %q", why)
	}
	AppendHistory(updates, HistoryLine{Kind: HistoryAuto, To: "v0.2.0", OK: true})
	if why := Skip(context.Background(), updates, rel); why != "" {
		t.Fatalf("after a success: %q", why)
	}

	// A release that declares a migration.
	if err := os.MkdirAll(filepath.Join(w.up, "docs", "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.up, "docs", "releases", "v0.3.0.migrate"), []byte("broker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, w.up, "add", ".")
	sh(t, w.up, "commit", "-qm", "three")
	sh(t, w.up, "tag", "v0.3.0")
	sh(t, w.clone, "fetch", "-q", "--tags", "origin")
	if why := Skip(context.Background(), t.TempDir(), Release{Tag: "v0.3.0", GitDir: dir}); !strings.Contains(why, "needs a broker migration") {
		t.Fatalf("a .migrate release: %q", why)
	}
}

// update --auto --check: the release found, waiting since its first check,
// each project judged; nothing staged, the clone untouched; up to date
// clears the waiting; a quiet test that cannot read fails, saying deferred.
func TestCheckPass(t *testing.T) {
	f := newFlow(t)
	d := f.deps(false)
	d.Holders = func(context.Context) ([]string, error) { return nil, nil }
	d.Judge = func(_ context.Context, p string) (Judged, error) {
		j := Judged{Now: autoNow}
		if p == "b" {
			j.Agents = []AgentFact{{"coder", "working", autoNow}}
		}
		return j, nil
	}
	d.AutoSetting = func() string { return "notify" }
	before := cloneState(t, f.w.clone)
	f.r.now = autoNow
	if code := CheckPass(context.Background(), d); code != 0 {
		t.Fatalf("exit %d: %s %s", code, f.out.String(), f.errw.String())
	}
	out := f.out.String()
	for _, w := range []string{"release v0.2.0 waiting since", "updates.auto is not install", "project a: quiet", "project b: in work (agent coder is working)"} {
		if !strings.Contains(out, w) {
			t.Errorf("lacks %q:\n%s", w, out)
		}
	}
	st := ReadAuto(d.updates())
	if st.Waiting == nil || st.Waiting.Tag != "v0.2.0" || !st.LastCheck.Equal(autoNow) {
		t.Fatalf("auto.json: %+v", st)
	}
	if cloneState(t, f.w.clone) != before {
		t.Error("the clone changed")
	}
	if _, err := os.Stat(d.treeFor("v0.2.0")); err == nil {
		t.Error("a check pass staged a tree")
	}
	if len(History(d.updates())) != 0 {
		t.Error("a check pass wrote history")
	}
	// Later: the waiting keeps its since.
	f.r.now = autoNow.Add(time.Hour)
	f.out.Reset()
	CheckPass(context.Background(), d)
	if st := ReadAuto(d.updates()); !st.Waiting.Since.Equal(autoNow) || !st.LastCheck.Equal(autoNow.Add(time.Hour)) {
		t.Fatalf("second check: %+v", st)
	}
	// Fail closed.
	d.Judge = func(context.Context, string) (Judged, error) { return Judged{}, errors.New("refused") }
	f.errw.Reset()
	if code := CheckPass(context.Background(), d); code != 1 || !strings.Contains(f.errw.String(), "a pass would be deferred") {
		t.Fatalf("unreadable: %d %s", code, f.errw.String())
	}
	// Up to date.
	d.Version = "v0.2.0"
	f.out.Reset()
	if code := CheckPass(context.Background(), d); code != 0 || !strings.Contains(f.out.String(), "up to date") {
		t.Fatalf("up to date: %d %s", code, f.out.String())
	}
	if st := ReadAuto(d.updates()); st.Waiting != nil {
		t.Fatalf("up to date keeps waiting: %+v", st.Waiting)
	}
}
