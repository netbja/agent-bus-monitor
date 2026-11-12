package rolecfg

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Grok's hook file (auto-state step 5, route b): turn start on
// UserPromptSubmit, turn end on Stop and StopFailure, each the one command
// given, whatever the agentbus path holds. StopCancelled is not registered:
// Grok sends it off the command loop, so it can arrive after the next turn's
// UserPromptSubmit and would flip a running turn idle.
func TestGrokHooksJSON(t *testing.T) {
	for _, bin := range []string{"/usr/local/bin/agentbus", "/opt/agent bus/agentbus", `/tmp/a"b\c/agentbus`, "/home/zoé/agentbus", "/tmp/<&>/agentbus"} {
		h := claudeHooks{TurnStart: ShellJoin([]string{bin, "roles", "turn", "start"}), TurnEnd: ShellJoin([]string{bin, "roles", "turn", "end"})}
		text := grokHooksJSON(h)
		var got struct {
			Hooks map[string][]struct {
				Matcher *string          `json:"matcher"`
				Hooks   []map[string]any `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("%q: %v\n%s", bin, err, text)
		}
		want := map[string]string{"UserPromptSubmit": h.TurnStart, "Stop": h.TurnEnd, "StopFailure": h.TurnEnd}
		if len(got.Hooks) != len(want) {
			t.Fatalf("%q: events %v, want %v", bin, got.Hooks, want)
		}
		for event, cmd := range want {
			g := got.Hooks[event]
			if len(g) != 1 || g[0].Matcher != nil || len(g[0].Hooks) != 1 {
				t.Fatalf("%q: %s = %+v, want one group of one hook", bin, event, g)
			}
			hk := g[0].Hooks[0]
			if hk["type"] != "command" || hk["command"] != cmd || hk["timeout"] != float64(5) || len(hk) != 3 {
				t.Errorf("%q: %s hook = %v, want command %q, timeout 5", bin, event, hk, cmd)
			}
		}
		if !strings.HasSuffix(text, "\n") || text != grokHooksJSON(h) {
			t.Errorf("%q: not one stable text ending in a newline", bin)
		}
	}
}

type grokHooksFixture struct {
	t                        *testing.T
	home, project, cache, ab string
	env                      []string
}

func newGrokHooksFixture(t *testing.T) *grokHooksFixture {
	root := t.TempDir()
	f := &grokHooksFixture{t: t, home: filepath.Join(root, "home"), project: filepath.Join(root, "home", "proj"), cache: filepath.Join(root, "cache")}
	must(t, os.MkdirAll(f.project, 0o755))
	f.env = []string{"HOME=" + f.home, "AGENT_LAUNCH_CACHE=" + f.cache}
	return f
}

func (f *grokHooksFixture) file() string {
	return filepath.Join(f.project, ".grok", "hooks", "agentbus.json")
}

func (f *grokHooksFixture) install(dir, text string) (string, error) {
	path, _, err := installGrokHooks(newEnv(f.env), dir, text)
	return path, err
}

func (f *grokHooksFixture) read() string {
	b, err := os.ReadFile(f.file())
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func TestInstallGrokHooksWritesOnlyItsOwnFile(t *testing.T) {
	f := newGrokHooksFixture(t)
	v1, v2 := "{\"hooks\":1}\n", "{\"hooks\":2}\n"

	// Absent: written, a project file anyone may read.
	if path, err := f.install(f.project, v1); err != nil || path != f.file() {
		t.Fatalf("absent: %q %v", path, err)
	}
	if st, err := os.Stat(f.file()); err != nil || f.read() != v1 || st.Mode().Perm() != 0o644 {
		t.Fatalf("written %q %v %v", f.read(), st.Mode(), err)
	}
	// The same text again: nothing to do.
	if _, err := f.install(f.project, v1); err != nil || f.read() != v1 {
		t.Fatalf("unchanged: %v", err)
	}
	// Our own unedited file with another text (a new agentbus path): replaced.
	if _, err := f.install(f.project, v2); err != nil || f.read() != v2 {
		t.Fatalf("ours, new text: %q %v", f.read(), err)
	}
	// Edited by the person since: never overwritten.
	must(t, os.WriteFile(f.file(), []byte("{\"hooks\":\"mine\"}\n"), 0o644))
	if _, err := f.install(f.project, v1); !errors.Is(err, errGrokHooksNotOurs) || f.read() != "{\"hooks\":\"mine\"}\n" {
		t.Fatalf("edited: %q %v", f.read(), err)
	}
	// A file agentbus never wrote (no record of it): never overwritten.
	g := newGrokHooksFixture(t)
	must(t, os.MkdirAll(filepath.Dir(g.file()), 0o755))
	must(t, os.WriteFile(g.file(), []byte("person\n"), 0o600))
	if _, err := g.install(g.project, v1); !errors.Is(err, errGrokHooksNotOurs) || g.read() != "person\n" {
		t.Fatalf("foreign: %q %v", g.read(), err)
	}
	// A lost record turns our own file into one we cannot prove: kept, never guessed.
	h := newGrokHooksFixture(t)
	if _, err := h.install(h.project, v1); err != nil {
		t.Fatal(err)
	}
	must(t, os.RemoveAll(h.cache))
	if _, err := h.install(h.project, v2); !errors.Is(err, errGrokHooksNotOurs) || h.read() != v1 {
		t.Fatalf("record lost: %q %v", h.read(), err)
	}
}

// A file that holds exactly our text but that agentbus did not write (the
// person's copy, or ours after its record was lost) is used as it is and never
// adopted: no record is written, so a later text never replaces it
// (reviewer-codex).
func TestInstallGrokHooksNeverAdoptsAnIdenticalFile(t *testing.T) {
	v1, v2 := "{\"hooks\":1}\n", "{\"hooks\":2}\n"
	// The person's own copy, identical to ours.
	f := newGrokHooksFixture(t)
	must(t, os.MkdirAll(filepath.Dir(f.file()), 0o755))
	must(t, os.WriteFile(f.file(), []byte(v1), 0o644))
	if _, err := f.install(f.project, v1); err != nil || f.read() != v1 {
		t.Fatalf("identical foreign file: %q %v", f.read(), err)
	}
	if _, err := f.install(f.project, v2); !errors.Is(err, errGrokHooksNotOurs) || f.read() != v1 {
		t.Fatalf("identical foreign file then v2: %q %v (adopted)", f.read(), err)
	}
	// Ours, the record lost, then the same version launched again, then v2.
	g := newGrokHooksFixture(t)
	if _, err := g.install(g.project, v1); err != nil {
		t.Fatal(err)
	}
	must(t, os.RemoveAll(g.cache))
	if _, err := g.install(g.project, v1); err != nil || g.read() != v1 {
		t.Fatalf("lost record, same text: %q %v", g.read(), err)
	}
	if _, err := g.install(g.project, v2); !errors.Is(err, errGrokHooksNotOurs) || g.read() != v1 {
		t.Fatalf("lost record, same text, then v2: %q %v (adopted)", g.read(), err)
	}
}

// Never ~/.grok (the person's own hooks and trust store), and never through a
// link: a .grok, hooks or agentbus.json that is a symlink is refused and its
// target untouched.
func TestInstallGrokHooksRefusesHomeAndLinks(t *testing.T) {
	text := "{\"hooks\":1}\n"
	f := newGrokHooksFixture(t)
	if _, err := f.install(f.home, text); err == nil {
		t.Fatal("the home folder accepted: that is ~/.grok")
	}
	if _, err := os.Lstat(filepath.Join(f.home, ".grok")); !os.IsNotExist(err) {
		t.Fatalf("~/.grok created: %v", err)
	}
	link := filepath.Join(filepath.Dir(f.home), "homelink")
	must(t, os.Symlink(f.home, link))
	if _, err := f.install(link, text); err == nil {
		t.Fatal("a link to the home folder accepted")
	}

	elsewhere := t.TempDir()
	for _, name := range []string{".grok", "hooks", "agentbus.json"} {
		g := newGrokHooksFixture(t)
		target := filepath.Join(elsewhere, name)
		var at string
		switch name {
		case ".grok":
			must(t, os.MkdirAll(target, 0o755))
			at = filepath.Join(g.project, ".grok")
		case "hooks":
			must(t, os.MkdirAll(target, 0o755))
			must(t, os.MkdirAll(filepath.Join(g.project, ".grok"), 0o755))
			at = filepath.Join(g.project, ".grok", "hooks")
		default:
			must(t, os.WriteFile(target, []byte("target\n"), 0o644))
			must(t, os.MkdirAll(filepath.Join(g.project, ".grok", "hooks"), 0o755))
			at = g.file()
		}
		must(t, os.Symlink(target, at))
		if _, err := g.install(g.project, text); err == nil {
			t.Errorf("%s as a symlink accepted", name)
		}
		if name == "agentbus.json" {
			if b, _ := os.ReadFile(target); string(b) != "target\n" {
				t.Errorf("link target rewritten: %q", b)
			}
		} else if _, err := os.Stat(filepath.Join(target, "agentbus.json")); err == nil {
			t.Errorf("%s: written through the link", name)
		} else if _, err := os.Stat(filepath.Join(target, "hooks", "agentbus.json")); err == nil {
			t.Errorf("%s: written through the link", name)
		}
	}
}

// An agentbus.json that is no regular file (a FIFO) is refused without being
// read: reading it would block the launch.
func TestInstallGrokHooksRefusesAFIFO(t *testing.T) {
	f := newGrokHooksFixture(t)
	must(t, os.MkdirAll(filepath.Dir(f.file()), 0o755))
	if err := syscall.Mkfifo(f.file(), 0o644); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.install(f.project, "{\"hooks\":1}\n"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, errGrokHooksNotOurs) {
			t.Fatalf("FIFO: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the launch blocked reading a FIFO")
	}
}

// A real launch writes the hook file in the folder Grok starts in, before the
// exec, with the same agentbus as every other hook; the argv is unchanged and
// a dry run writes nothing.
func TestLaunchGrokWritesTurnHooks(t *testing.T) {
	f := newLaunchFixture(t)
	dir := filepath.Join(f.root, "project")
	must(t, os.MkdirAll(dir, 0o755))
	withLaunchDir(t, dir)
	f.env = append(f.env, "AGENTBUS_BIN=/opt/bus/agentbus")
	m := f.config("grok", "", "", "")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	if err != nil || !strings.Contains(out, "grok --model test-model --permission-mode default --session-id '<new session id>' --rules '<role prompt and skill paths>'\n") {
		t.Fatalf("dry run = %q %v", out, err)
	}
	file := filepath.Join(dir, ".grok", "hooks", "agentbus.json")
	if _, err := os.Lstat(filepath.Join(dir, ".grok")); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote .grok: %v", err)
	}
	_, errOut, err := f.launch(m, "coder")
	if err != nil {
		t.Fatal(err, errOut)
	}
	want := grokHooksJSON(launchHooks(newEnv(f.env)))
	if b, err := os.ReadFile(file); err != nil || string(b) != want || !strings.Contains(string(b), "/opt/bus/agentbus roles turn start") {
		t.Fatalf("hook file = %q %v, want %q", b, err, want)
	}
	if !strings.Contains(errOut, file) || !strings.Contains(errOut, "trust") {
		t.Errorf("stderr does not name the file and the trust to give: %q", errOut)
	}
	if got := f.execed[1 : len(f.execed)-1]; len(got) != 8 || strings.Join(append(got[:6:6], got[7]), " ") != "grok --model test-model --permission-mode default --session-id --rules" {
		t.Fatalf("argv changed: %q", got)
	}
}

// The person's own agentbus.json is never overwritten, and a hook file that
// cannot be written never stops the agent: it starts, and stderr says why it
// has no turn hooks.
func TestLaunchGrokKeepsAPersonsFile(t *testing.T) {
	f := newLaunchFixture(t)
	dir := filepath.Join(f.root, "project")
	file := filepath.Join(dir, ".grok", "hooks", "agentbus.json")
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	must(t, os.WriteFile(file, []byte("mine\n"), 0o644))
	withLaunchDir(t, dir)
	_, errOut, err := f.launch(f.config("grok", "", "", ""), "coder")
	if err != nil || len(f.execed) == 0 {
		t.Fatalf("the agent did not start: %v %q", err, errOut)
	}
	if b, _ := os.ReadFile(file); string(b) != "mine\n" {
		t.Fatalf("the person's file changed: %q", b)
	}
	if !strings.Contains(errOut, "warning:") || !strings.Contains(errOut, file) {
		t.Errorf("no warning naming the file: %q", errOut)
	}
}

// Other clients get no Grok file.
func TestOnlyGrokGetsTheHookFile(t *testing.T) {
	for _, client := range []string{"claude-code", "codex", "kimi-cli"} {
		f := newLaunchFixture(t)
		dir := filepath.Join(f.root, "project")
		must(t, os.MkdirAll(dir, 0o755))
		withLaunchDir(t, dir)
		if _, e, err := f.launch(f.config(client, "", "", ""), "coder"); err != nil {
			t.Fatal(client, err, e)
		}
		if _, err := os.Lstat(filepath.Join(dir, ".grok")); !os.IsNotExist(err) {
			t.Errorf("%s: .grok written", client)
		}
	}
}

// The hook file holds this machine's agentbus path: where the launch wrote it
// in a Git checkout, the repository's own local exclude lists it (never a
// .gitignore, which would be committed), once, at its path from the top, in
// the common git dir for a linked worktree. Outside a checkout nothing else
// is written; a person's file is never listed.
func TestGrokHooksKeptOutOfGit(t *testing.T) {
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	text := "{\"hooks\":1}\n"
	f := newGrokHooksFixture(t)
	git(f.project, "init", "-q")
	sub := filepath.Join(f.project, "pkg")
	must(t, os.MkdirAll(sub, 0o755))
	for i := 0; i < 2; i++ { // the second launch adds no second line
		if _, _, err := installGrokHooks(newEnv(f.env), f.project, text); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := installGrokHooks(newEnv(f.env), sub, text); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(f.project, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/.grok/hooks/agentbus.json", "/pkg/.grok/hooks/agentbus.json"} {
		if n := strings.Count(string(b), "\n"+line+"\n"); n != 1 {
			t.Errorf("exclude lists %s %d times:\n%s", line, n, b)
		}
	}
	if st := git(f.project, "status", "--porcelain", "--untracked-files=all"); strings.Contains(st, ".grok") {
		t.Errorf("git status still shows the hook file:\n%s", st)
	}
	if _, err := os.Lstat(filepath.Join(f.project, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("a .gitignore was written: %v", err)
	}

	// A linked worktree: the exclude Git reads is the common one.
	git(f.project, "commit", "-q", "--allow-empty", "-m", "c")
	wt := filepath.Join(f.home, "wt")
	git(f.project, "worktree", "add", "-q", wt)
	if _, _, err := installGrokHooks(newEnv(f.env), wt, text); err != nil {
		t.Fatal(err)
	}
	if st := git(wt, "status", "--porcelain", "--untracked-files=all"); strings.Contains(st, ".grok") {
		t.Errorf("worktree: git status still shows the hook file:\n%s", st)
	}

	// Outside a checkout: written, nothing else.
	g := newGrokHooksFixture(t)
	if _, _, err := installGrokHooks(newEnv(g.env), g.project, text); err != nil {
		t.Fatal(err)
	}
	// A person's file in a checkout: kept, and never listed.
	h := newGrokHooksFixture(t)
	git(h.project, "init", "-q")
	must(t, os.MkdirAll(filepath.Dir(h.file()), 0o755))
	must(t, os.WriteFile(h.file(), []byte("mine\n"), 0o644))
	if _, _, err := installGrokHooks(newEnv(h.env), h.project, text); !errors.Is(err, errGrokHooksNotOurs) {
		t.Fatalf("person's file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.project, ".git", "info", "exclude")); strings.Contains(string(b), "agentbus.json") {
		t.Errorf("a person's file was listed in the exclude:\n%s", b)
	}
	// Nor a person's file identical to ours: used, not ours, not listed.
	must(t, os.WriteFile(h.file(), []byte(text), 0o644))
	if _, ours, err := installGrokHooks(newEnv(h.env), h.project, text); err != nil || ours {
		t.Fatalf("identical person's file: ours=%v %v", ours, err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.project, ".git", "info", "exclude")); strings.Contains(string(b), "agentbus.json") {
		t.Errorf("an identical person's file was listed in the exclude:\n%s", b)
	}
}

// An exclude line is a gitignore pattern, not a path: a folder named with
// [ ] * ? or a backslash is written escaped, so the line matches that file
// and no sibling, and the result is checked with git check-ignore. A path
// that holds a line break cannot be one line: refused, the exclude untouched
// (foureyes, reviewer-codex).
func TestGrokHooksExcludeEscapesPatterns(t *testing.T) {
	git := func(dir string, args ...string) (string, error) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := c.CombinedOutput()
		return string(out), err
	}
	text := "{\"hooks\":1}\n"
	f := newGrokHooksFixture(t)
	if out, err := git(f.project, "init", "-q"); err != nil {
		t.Fatal(out, err)
	}
	// Each odd folder, with the sibling its unescaped pattern would match instead.
	for _, c := range []struct{ odd, sibling string }{
		{"pkg[1]", "pkg1"}, {"a*b", "axyb"}, {"q?", "qz"}, {`back\slash`, "backslash"},
	} {
		dir := filepath.Join(f.project, c.odd)
		must(t, os.MkdirAll(dir, 0o755))
		if _, _, err := installGrokHooks(newEnv(f.env), dir, text); err != nil {
			t.Fatalf("%s: %v", c.odd, err)
		}
		other := filepath.Join(f.project, c.sibling, ".grok", "hooks", "agentbus.json")
		must(t, os.MkdirAll(filepath.Dir(other), 0o755))
		must(t, os.WriteFile(other, []byte("person\n"), 0o644))
		rel := filepath.Join(c.odd, ".grok", "hooks", "agentbus.json")
		if out, err := git(f.project, "check-ignore", "-q", "--no-index", rel); err != nil {
			t.Errorf("%s: our file is not ignored: %v %s", c.odd, err, out)
		}
		if _, err := git(f.project, "check-ignore", "-q", "--no-index", filepath.Join(c.sibling, ".grok", "hooks", "agentbus.json")); err == nil {
			t.Errorf("%s: the sibling %s is ignored too", c.odd, c.sibling)
		}
	}
	// The project's own .gitignore re-includes it (it outranks info/exclude):
	// check-ignore says so, and the launch warns instead of claiming it.
	g := newGrokHooksFixture(t)
	if out, err := git(g.project, "init", "-q"); err != nil {
		t.Fatal(out, err)
	}
	must(t, os.WriteFile(filepath.Join(g.project, ".gitignore"), []byte("!**/agentbus.json\n"), 0o644))
	if _, _, err := installGrokHooks(newEnv(g.env), g.project, text); !errors.Is(err, errGrokExclude) {
		t.Fatalf("re-included by .gitignore: %v, want errGrokExclude", err)
	}

	before, _ := os.ReadFile(filepath.Join(f.project, ".git", "info", "exclude"))
	nl := filepath.Join(f.project, "two\nlines")
	must(t, os.MkdirAll(nl, 0o755))
	if _, _, err := installGrokHooks(newEnv(f.env), nl, text); !errors.Is(err, errGrokExclude) {
		t.Fatalf("a line break in the path: %v, want errGrokExclude", err)
	}
	if after, _ := os.ReadFile(filepath.Join(f.project, ".git", "info", "exclude")); string(after) != string(before) {
		t.Errorf("the exclude changed for a path with a line break:\n%s", after)
	}
}

func withLaunchDir(t *testing.T, dir string) {
	t.Helper()
	old := launchDir
	launchDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { launchDir = old })
}
