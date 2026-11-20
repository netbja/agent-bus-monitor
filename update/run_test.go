package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/setup"
)

// flow is an update world: an upstream whose tags carry roles.toml, a
// repository skill and the scripts; the person's clone of it; a bin dir with
// the running (old) binaries and setup's agent-launch link; a home whose
// external skills folder is the person's (no setup marker), so the update
// leaves it alone.
type flow struct {
	w             world
	home, bin, up string
	env           map[string]string
	r             *fakeRun
	out, errw     bytes.Buffer
	services      []Service
}

const rolesToml = "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n[roles.coder]\ntier = \"boot\"\nskills = [\"review\"]\n"

func writeFile(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	up := t.TempDir()
	sh(t, up, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(up, "roles.toml"), rolesToml, 0o644)
	writeFile(t, filepath.Join(up, "skills", "review", "SKILL.md"), "# review v1\n", 0o644)
	writeFile(t, filepath.Join(up, "scripts", "link-role-skills.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(up, "scripts", "agent-launch"), "#!/bin/sh\n", 0o755)
	sh(t, up, "add", ".")
	sh(t, up, "commit", "-qm", "one")
	sh(t, up, "tag", "v0.1.0")
	v1 := sh(t, up, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(up, "skills", "review", "SKILL.md"), "# review v2\n", 0o644)
	writeFile(t, filepath.Join(up, "docs", "releases", "v0.2.0.md"), "notes\n", 0o644)
	sh(t, up, "add", ".")
	sh(t, up, "commit", "-qm", "two")
	sh(t, up, "tag", "v0.2.0")
	clone := filepath.Join(t.TempDir(), "clone")
	sh(t, filepath.Dir(clone), "clone", "-q", up, clone)
	sh(t, clone, "checkout", "-q", "-b", "mine", v1)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	for _, n := range []string{"agentbus", "busmon"} {
		writeFile(t, filepath.Join(bin, n), "old "+n, 0o755)
	}
	if err := os.Symlink(filepath.Join(clone, "scripts", "agent-launch"), filepath.Join(bin, "agent-launch")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".local", "share", "agentbus", "skills", "mattpocock", "README"), "the person's\n", 0o644)
	f := &flow{w: world{up: up, clone: clone, v1: v1}, home: home, bin: bin, up: up,
		env:      map[string]string{"HOME": home, "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_DATA_HOME": filepath.Join(home, ".local", "share")},
		services: []Service{{"a", "dispatch", "old"}, {"a", "missions", "old"}, {"b", "dispatch", "old"}}}
	f.r = &fakeRun{t: t, f: f, recorded: map[string]string{"a dispatch": "old agentbus", "a missions": "old agentbus", "b dispatch": "old agentbus"}}
	return f
}

func (f *flow) deps(tty bool) Deps {
	var environ []string
	for k, v := range f.env {
		environ = append(environ, k+"="+v)
	}
	d := Deps{R: f.r, Getenv: func(k string) string { return f.env[k] }, Environ: environ,
		Now: func() time.Time { return f.r.now }, Sleep: func(d time.Duration) { f.r.now = f.r.now.Add(d) },
		Out: &f.out, Err: &f.errw, Version: "v0.1.0", Repo: f.w.clone, BinDir: f.bin, FromBuild: "v0.1.0.old",
		Running: func(context.Context) ([]Service, error) { return f.services, nil }, Settle: 60 * time.Second}
	if tty {
		d.TTY = &rw{in: strings.NewReader("y\n")}
	}
	return d
}

type rw struct {
	in  *strings.Reader
	out bytes.Buffer
}

func (r *rw) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *rw) Write(p []byte) (int, error) { return r.out.Write(p) }

// fakeRun fakes every command but git: go build writes "new <name>"; the
// built binaries answer their version; the link script links the role skill
// into the tree; service restart records what the bin dir's agentbus holds;
// service stale lists each service whose record differs from it.
type fakeRun struct {
	t         *testing.T
	f         *flow
	now       time.Time
	ran       []string
	recorded  map[string]string
	failLink  bool
	wrongLink bool   // the link script succeeds but links the clone's skill, not the tree's
	neverOK   string // "<project> <kind>": never records a build
	restarted []string
}

func (r *fakeRun) LookPath(name string) (string, error) { return "", errors.New("no") }

func (r *fakeRun) Run(_ context.Context, c setup.Cmd) ([]byte, error) {
	key := filepath.Base(c.Name) + " " + strings.Join(c.Args, " ")
	r.ran = append(r.ran, key)
	switch {
	case c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "build":
		for i, a := range c.Args {
			if a == "-o" {
				writeFile(r.t, c.Args[i+1], "new "+filepath.Base(c.Args[i+1]), 0o755)
			}
		}
		return nil, nil
	case c.Name == "git" && len(c.Args) > 3 && (c.Args[2] == "fetch" || c.Args[2] == "checkout"):
		return nil, nil // setup's skills clone: fetched and checked out "successfully", never moved
	case key == "agentbus version" || key == "busmon --version":
		return []byte("ok\n"), nil
	case filepath.Base(c.Name) == "link-role-skills.sh":
		if r.failLink {
			return []byte("skill not found\n"), errors.New("exit 1")
		}
		tree := filepath.Dir(filepath.Dir(c.Name))
		if r.wrongLink {
			tree = r.f.w.clone
		}
		dest := filepath.Join(r.f.home, ".agents", "skills")
		_ = os.MkdirAll(dest, 0o755)
		_ = relink(filepath.Join(dest, "review"), filepath.Join(tree, "skills", "review"))
		return nil, nil
	case c.Name == filepath.Join(r.f.bin, "agentbus") && len(c.Args) == 5 && c.Args[2] == "service" && c.Args[3] == "restart":
		s := c.Args[1] + " " + c.Args[4]
		r.restarted = append(r.restarted, s)
		if s != r.neverOK {
			b, _ := os.ReadFile(filepath.Join(r.f.bin, "agentbus"))
			r.recorded[s] = string(b)
		}
		return nil, nil
	case c.Name == filepath.Join(r.f.bin, "agentbus") && strings.Join(c.Args, " ") == "service stale":
		b, _ := os.ReadFile(filepath.Join(r.f.bin, "agentbus"))
		var out []string
		for s, build := range r.recorded {
			if build != string(b) {
				out = append(out, s+" x")
			}
		}
		return []byte(strings.Join(out, "\n")), nil
	}
	r.t.Fatalf("unexpected command: %s %v", c.Name, c.Args)
	return nil, nil
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func cloneState(t *testing.T, clone string) string {
	return sh(t, clone, "rev-parse", "--abbrev-ref", "HEAD") + " " + sh(t, clone, "rev-parse", "HEAD") + " [" + sh(t, clone, "status", "--porcelain") + "]"
}

// TestUpdate: the newest release built in its own tree, the clone untouched,
// both binaries and the agent-launch link switched, the skill linked into the
// tree, --project a's services restarted and green, b's listed with their
// command and never restarted, a history line, the snapshot kept.
func TestUpdate(t *testing.T) {
	f := newFlow(t)
	before := cloneState(t, f.w.clone)
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	tree := filepath.Join(f.home, ".local", "share", "agentbus", "src", "v0.2.0")
	if cloneState(t, f.w.clone) != before {
		t.Fatalf("clone moved: %s -> %s", before, cloneState(t, f.w.clone))
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "new agentbus" || read(t, filepath.Join(f.bin, "busmon")) != "new busmon" ||
		read(t, filepath.Join(tree, "agentbus")) != "new agentbus" {
		t.Fatal("binaries not installed")
	}
	if l, _ := os.Readlink(filepath.Join(f.bin, "agent-launch")); l != filepath.Join(tree, "scripts", "agent-launch") {
		t.Fatalf("agent-launch -> %s", l)
	}
	if read(t, filepath.Join(f.home, ".agents", "skills", "review", "SKILL.md")) != "# review v2\n" {
		t.Fatal("skill not linked into the tree")
	}
	if strings.Join(f.r.restarted, ",") != "a dispatch,a missions" {
		t.Fatalf("restarted %v", f.r.restarted)
	}
	out := f.out.String()
	for _, want := range []string{"agentbus v0.1.0 -> v0.2.0  (release notes: " + filepath.Join(tree, "docs", "releases", "v0.2.0.md") + ")",
		"build from   " + tree + "  (your clone is not touched)", "restart      a dispatch, a missions",
		"not touched  b dispatch (old): agentbus --project b service restart dispatch", "skills changed: review",
		"not restarted: b dispatch (old): agentbus --project b service restart dispatch"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	updates := Dir(f.env["XDG_STATE_HOME"])
	h := History(updates)
	if len(h) != 1 || !h[0].OK || h[0].From != "v0.1.0" || h[0].To != "v0.2.0" {
		t.Fatalf("history %+v", h)
	}
	m, err := ReadManifest(filepath.Join(updates, h[0].Snapshot))
	if err != nil || m.AgentLaunch != filepath.Join(f.w.clone, "scripts", "agent-launch") || len(m.Services) != 2 || m.Skills.Home == "" || m.Skills.Ours || m.Skills.Absent {
		t.Fatalf("manifest %+v %v", m, err)
	}
	if read(t, filepath.Join(updates, h[0].Snapshot, "bin", "agentbus")) != "old agentbus" {
		t.Fatal("the snapshot does not hold the old binary")
	}
}

// TestUpdateServiceRedRollsBack: a service that never records the new build
// rolls everything back, byte-equal: binaries, link, skills, and the
// services restarted again on the old build; history ok=false, exit 1.
func TestUpdateServiceRedRollsBack(t *testing.T) {
	f := newFlow(t)
	f.r.neverOK = "a missions"
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	f.assertRolledBack(t)
	if strings.Join(f.r.restarted, ",") != "a dispatch,a missions,a dispatch,a missions" {
		t.Fatalf("restarted %v", f.r.restarted)
	}
	if !strings.Contains(f.errw.String(), "a missions did not record the new build within 1m0s") {
		t.Fatalf("%s", f.errw.String())
	}
}

// TestUpdateSkillsFailRollsBack: a failed role skill link rolls back.
func TestUpdateSkillsFailRollsBack(t *testing.T) {
	f := newFlow(t)
	f.r.failLink = true
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	f.assertRolledBack(t)
	if len(f.r.restarted) != 0 {
		t.Fatalf("a service restarted before the skills passed: %v", f.r.restarted)
	}
}

func (f *flow) assertRolledBack(t *testing.T) {
	t.Helper()
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || read(t, filepath.Join(f.bin, "busmon")) != "old busmon" {
		t.Fatal("binaries not restored")
	}
	if l, _ := os.Readlink(filepath.Join(f.bin, "agent-launch")); l != filepath.Join(f.w.clone, "scripts", "agent-launch") {
		t.Fatalf("agent-launch -> %s", l)
	}
	if _, err := os.Lstat(filepath.Join(f.home, ".agents", "skills", "review")); !os.IsNotExist(err) {
		t.Fatal("the link the release added was left")
	}
	h := History(Dir(f.env["XDG_STATE_HOME"]))
	if len(h) != 1 || h[0].OK {
		t.Fatalf("history %+v", h)
	}
}

// TestUpdateCheckAndRefusals: --check writes nothing; no terminal and no
// --yes refuses; up to date says so; an interrupted update names --rollback.
func TestUpdateCheckAndRefusals(t *testing.T) {
	f := newFlow(t)
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Check: true}); code != 0 || !strings.Contains(f.out.String(), "-> v0.2.0") {
		t.Fatalf("check: %d %s", code, f.out.String())
	}
	if code := Run(context.Background(), f.deps(false), Options{Project: "a"}); code != 1 || !strings.Contains(f.errw.String(), "no terminal") {
		t.Fatalf("no tty: %d %s", code, f.errw.String())
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || len(Snapshots(Dir(f.env["XDG_STATE_HOME"]))) != 0 {
		t.Fatal("a refused update changed something")
	}
	d := f.deps(false)
	d.Version = "v0.2.0"
	f.out.Reset()
	if code := Run(context.Background(), d, Options{}); code != 0 || !strings.Contains(f.out.String(), "up to date") {
		t.Fatalf("up to date: %d %s", code, f.out.String())
	}
	updates := Dir(f.env["XDG_STATE_HOME"])
	if err := WriteSnapshot(filepath.Join(updates, "x1"), Manifest{BinDir: f.bin}); err != nil {
		t.Fatal(err)
	}
	f.errw.Reset()
	if code := Run(context.Background(), f.deps(true), Options{}); code != 1 || !strings.Contains(f.errw.String(), "agentbus update --rollback x1") {
		t.Fatalf("interrupted: %d %s", code, f.errw.String())
	}
}

// TestUpdateAsksOnTheTerminal: the plan, then one question; y installs.
func TestUpdateAsksOnTheTerminal(t *testing.T) {
	f := newFlow(t)
	d := f.deps(true)
	if code := Run(context.Background(), d, Options{}); code != 0 {
		t.Fatalf("exit %d %s", code, f.errw.String())
	}
	if !strings.Contains(d.TTY.(*rw).out.String(), "Install v0.2.0? [y/N]") || len(f.r.restarted) != 0 {
		t.Fatalf("asked %q, restarted %v", d.TTY.(*rw).out.String(), f.r.restarted)
	}
}

// TestRestore: --restore by the snapshot's binary puts the update back.
func TestRestore(t *testing.T) {
	f := newFlow(t)
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 0 {
		t.Fatalf("update %d %s", code, f.errw.String())
	}
	updates := Dir(f.env["XDG_STATE_HOME"])
	id := Snapshots(updates)[0]
	f.r.restarted = nil
	if code := Restore(context.Background(), f.deps(false), id); code != 0 {
		t.Fatalf("restore %d %s", code, f.errw.String())
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || strings.Join(f.r.restarted, ",") != "a dispatch,a missions" {
		t.Fatalf("not restored: %v", f.r.restarted)
	}
	if _, err := os.Lstat(filepath.Join(f.home, ".agents", "skills", "review")); !os.IsNotExist(err) {
		t.Fatal("the release's skill link stayed")
	}
	if h := History(updates); len(h) != 2 || h[1].Kind != "rollback" || !h[1].OK {
		t.Fatalf("history %+v", h)
	}
}

// TestUpdateWrongSkillLinkRollsBack: a link script that succeeds but leaves
// a role skill resolving elsewhere than its source in the new tree fails the
// strict check, and rolls back (update spec §3.4 step 5, §7).
func TestUpdateWrongSkillLinkRollsBack(t *testing.T) {
	f := newFlow(t)
	f.r.wrongLink = true
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" || !strings.Contains(f.errw.String(), "does not resolve to") {
		t.Fatalf("not rolled back: %s", f.errw.String())
	}
}

// TestUpdateSkillsCloneOffPinRollsBack: setup's own skills clone (its
// marker) that is not at the pin after the step fails the check: rolled
// back, the clone back on its recorded HEAD.
func TestUpdateSkillsCloneOffPinRollsBack(t *testing.T) {
	f := newFlow(t)
	home := filepath.Join(f.home, ".local", "share", "agentbus", "skills", "mattpocock")
	_ = os.RemoveAll(home)
	_ = os.MkdirAll(home, 0o755)
	sh(t, home, "init", "-q", "-b", "main")
	commit(t, home, "pin")
	sh(t, home, "tag", "v1.3.1")
	head := commit(t, home, "after the pin")
	writeFile(t, filepath.Join(home, ".git", "agentbus-setup"), "", 0o644)
	if code := Run(context.Background(), f.deps(false), Options{Project: "a", Yes: true}); code != 1 {
		t.Fatalf("exit %d\n%s", code, f.errw.String())
	}
	if !strings.Contains(f.errw.String(), "are not at v1.3.1") || read(t, filepath.Join(f.bin, "agentbus")) != "old agentbus" {
		t.Fatalf("%s", f.errw.String())
	}
	if sh(t, home, "rev-parse", "HEAD") != head {
		t.Fatal("the clone is not back on its recorded HEAD")
	}
}
