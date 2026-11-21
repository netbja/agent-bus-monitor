package mergequeue

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Spec 2026-11-21-conflict-owner-design r5, §2 (detection) and §3 (the
// owner): build PR A, nothing sent.

// conflictWorld is newGitWorld plus, on origin: feat/clean (a new file),
// feat/clash (f changed one way) and main moved (f changed the other way),
// so feat/clash conflicts on f and feat/clean merges.
type conflictWorld struct {
	gitWorld
	mainTip, clean, clash string
}

func newConflictWorld(t *testing.T) conflictWorld {
	t.Helper()
	w := conflictWorld{gitWorld: newGitWorld(t)}
	branch := func(name, file, body string) string {
		git(t, w.env, w.pusher, "switch", "-q", "-c", name, "main")
		os.WriteFile(filepath.Join(w.pusher, file), []byte(body), 0o644)
		git(t, w.env, w.pusher, "add", file)
		git(t, w.env, w.pusher, "commit", "-q", "-m", name)
		git(t, w.env, w.pusher, "push", "-q", "origin", name)
		return git(t, w.env, w.pusher, "rev-parse", "HEAD")
	}
	w.clean = branch("feat/clean", "c", "c\n")
	w.clash = branch("feat/clash", "f", "clash\n")
	git(t, w.env, w.pusher, "switch", "-q", "main")
	os.WriteFile(filepath.Join(w.pusher, "f"), []byte("main moved\n"), 0o644)
	git(t, w.env, w.pusher, "commit", "-q", "-am", "main moves")
	git(t, w.env, w.pusher, "push", "-q", "origin", "main")
	w.mainTip = git(t, w.env, w.pusher, "rev-parse", "HEAD")
	return w
}

func cands(w conflictWorld) []Candidate {
	return []Candidate{{Key: "feat/clean", Branch: "feat/clean", Head: w.clean}, {Key: "feat/clash", Branch: "feat/clash", Head: w.clash}}
}

// A clean head is clean, a conflicting one names its files; the heads are
// fetched (they are not in work's store) without writing a ref; the
// scratch repository is gone after.
func TestCheckConflicts(t *testing.T) {
	w := newConflictWorld(t)
	ctx := context.Background()
	if err := FetchBase(ctx, w.work, w.env, "main"); err != nil {
		t.Fatal(err)
	}
	before := git(t, w.env, w.work, "for-each-ref")
	scratch := t.TempDir()
	got, why := CheckConflicts(ctx, ConflictCheck{Dir: w.work, Scratch: scratch, Env: w.env, Fetch: true}, w.mainTip, cands(w))
	if why != "" {
		t.Fatalf("why %q", why)
	}
	if j := got["feat/clean"]; j.Why != "" || j.Conflict {
		t.Fatalf("clean: %+v", j)
	}
	if j := got["feat/clash"]; j.Why != "" || !j.Conflict || !slices.Equal(j.Files, []string{"f"}) {
		t.Fatalf("clash: %+v", j)
	}
	if after := git(t, w.env, w.work, "for-each-ref"); after != before {
		t.Fatalf("refs moved:\n%s\n---\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(w.work, ".git", "FETCH_HEAD")); err == nil {
		t.Fatal("FETCH_HEAD written")
	}
	if es, _ := os.ReadDir(scratch); len(es) != 0 {
		t.Fatalf("scratch left: %v", es)
	}
}

// A head absent here and from origin is skipped with a reason, never judged
// clean; local mode fetches nothing.
func TestCheckConflictsAbsentHead(t *testing.T) {
	w := newConflictWorld(t)
	ctx := context.Background()
	FetchBase(ctx, w.work, w.env, "main")
	ghost := strings.Repeat("ab", 20)
	got, _ := CheckConflicts(ctx, ConflictCheck{Dir: w.work, Scratch: t.TempDir(), Env: w.env, Fetch: true}, w.mainTip,
		[]Candidate{{Key: "feat/ghost", Branch: "feat/ghost", Head: ghost}})
	if j := got["feat/ghost"]; j.Why == "" || j.Conflict {
		t.Fatalf("absent head: %+v", j)
	}
	// Local mode: no fetch, so origin's heads stay absent here.
	got, _ = CheckConflicts(ctx, ConflictCheck{Dir: w.work, Scratch: t.TempDir(), Env: w.env}, w.mainTip, cands(w))
	if j := got["feat/clash"]; j.Why == "" {
		t.Fatalf("local mode fetched: %+v", j)
	}
	if out := git(t, w.env, w.work, "cat-file", "-t", w.mainTip); out != "commit" {
		t.Fatal("the base is absent")
	}
}

// A git without merge-tree --write-tree or --attr-source is judged by its
// usage text: a reason, nothing judged.
func TestMergeTreeProbe(t *testing.T) {
	for _, c := range []struct {
		out  string
		want bool
	}{
		{"usage: git merge-tree [--write-tree] [<options>] <branch1> <branch2>\n", true},
		{"error: unknown option `attr-source=4b825dc'\nusage: git [-v | --version]", false},
		{"usage: git merge-tree <base-tree> <branch1> <branch2>\n", false},
	} {
		if got := mergeTreeUsable(c.out); got != c.want {
			t.Errorf("%q: %v", c.out, got)
		}
	}
	fake := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n*merge-tree*) echo \"usage: git merge-tree <base-tree> <branch1> <branch2>\" >&2; exit 129;;\n*hash-object*) echo 4b825dc642cb6eb9a060e54bf8d69288fbee4904;;\n*init*) /bin/mkdir -p \"$5/objects/info\";;\nesac\nexit 0\n"
	os.WriteFile(filepath.Join(fake, "git"), []byte(script), 0o755)
	env := []string{"PATH=" + fake}
	got, why := CheckConflicts(context.Background(), ConflictCheck{Dir: t.TempDir(), Scratch: t.TempDir(), Env: env}, strings.Repeat("a", 40),
		[]Candidate{{Key: "x", Branch: "x", Head: strings.Repeat("b", 40)}})
	if why != gitTooOld || len(got) != 0 {
		t.Fatalf("old git: %v %q", got, why)
	}
}

// No repository code runs (§2): a merge driver that creates a file, chosen
// four ways, never runs in the check, which still finds the conflict; a
// control run of merge-tree in the repository itself does run it.
func TestCheckConflictsRunsNoDriver(t *testing.T) {
	for _, way := range []string{"gitattributes", "merge.default", "info/attributes", "core.attributesFile"} {
		t.Run(way, func(t *testing.T) {
			w := newConflictWorld(t)
			ctx := context.Background()
			FetchBase(ctx, w.work, w.env, "main")
			git(t, w.env, w.work, "fetch", "-q", "--no-write-fetch-head", "--no-tags", "--refmap=", "origin", "feat/clash")
			marker := filepath.Join(t.TempDir(), "driver-ran")
			git(t, w.env, w.work, "config", "merge.evil.driver", "touch "+marker+"; false")
			env := w.env
			switch way {
			case "gitattributes": // in the tree of both sides: on main and on the branch
				for _, br := range []string{"main", "feat/clash"} {
					git(t, w.env, w.pusher, "switch", "-q", br)
					os.WriteFile(filepath.Join(w.pusher, ".gitattributes"), []byte("f merge=evil\n"), 0o644)
					git(t, w.env, w.pusher, "add", ".gitattributes")
					git(t, w.env, w.pusher, "commit", "-q", "-m", "attrs")
					git(t, w.env, w.pusher, "push", "-q", "origin", br)
				}
				FetchBase(ctx, w.work, w.env, "main")
				git(t, w.env, w.work, "fetch", "-q", "--no-write-fetch-head", "--no-tags", "--refmap=", "origin", "feat/clash")
				w.mainTip = git(t, w.env, w.pusher, "rev-parse", "main")
				w.clash = git(t, w.env, w.pusher, "rev-parse", "feat/clash")
				// The services' checkout of that tree holds the file too: a
				// non-bare repository reads its attributes there.
				os.WriteFile(filepath.Join(w.work, ".gitattributes"), []byte("f merge=evil\n"), 0o644)
			case "merge.default":
				git(t, w.env, w.work, "config", "merge.default", "evil")
			case "info/attributes":
				os.MkdirAll(filepath.Join(w.work, ".git", "info"), 0o755)
				os.WriteFile(filepath.Join(w.work, ".git", "info", "attributes"), []byte("f merge=evil\n"), 0o644)
			case "core.attributesFile":
				home := t.TempDir()
				attrs := filepath.Join(home, "attrs")
				os.WriteFile(attrs, []byte("f merge=evil\n"), 0o644)
				os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\tattributesFile = "+attrs+"\n[merge \"evil\"]\n\tdriver = touch "+marker+"; false\n"), 0o644)
				env = append(slices.DeleteFunc(slices.Clone(w.env), func(kv string) bool {
					return strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=")
				}), "HOME="+home)
			}
			got, why := CheckConflicts(ctx, ConflictCheck{Dir: w.work, Scratch: t.TempDir(), Env: env}, w.mainTip,
				[]Candidate{{Key: "feat/clash", Branch: "feat/clash", Head: w.clash}})
			if j := got["feat/clash"]; why != "" || !j.Conflict || !slices.Contains(j.Files, "f") {
				t.Fatalf("check: %+v %q", j, why)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the driver ran in the check")
			}
			// Control: the same merge in the repository itself runs it.
			cmd := exec.Command("git", "merge-tree", "--write-tree", "--name-only", "--no-messages", w.mainTip, w.clash)
			cmd.Dir, cmd.Env = w.work, env
			cmd.Run()
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("control: the driver did not run (%v), so the test could not see it", err)
			}
		})
	}
}

// §3: the owner from the board (claim branch or request branch), else the
// branch's <role>/ prefix when it is a known agent; master otherwise.
func TestConflictOwner(t *testing.T) {
	board := map[string]bus.BoardEntry{
		"t1": {Owner: "coder", Branch: "feat/claimed"},
		"t2": {Owner: "coder2", Request: &bus.RequestInfo{Branch: "fix/requested"}},
		"t3": {Owner: "coder", Branch: "feat/twice"},
		"t4": {Owner: "coder", Request: &bus.RequestInfo{Branch: "feat/twice"}},
		"t5": {Owner: "coder", Branch: "feat/shared"},
		"t6": {Owner: "coder2", Branch: "feat/shared"},
		"t7": {Owner: "Not An Agent", Branch: "feat/odd"},
		"t8": {Owner: "ghost", Branch: "feat/gone"},
	}
	agents := map[string]bool{"coder": true, "coder2": true, "docs": true, "master": true}
	for branch, want := range map[string]string{
		"feat/claimed":  "coder",
		"fix/requested": "coder2",
		"feat/twice":    "coder", // two entries, one owner
		"feat/shared":   "master",
		"feat/odd":      "master",
		"feat/gone":     "master", // an owner that is no agent of the project
		"coder/x":       "coder",  // no entry: the prefix, a known agent
		"docs/readme":   "docs",
		"feat/x":        "master", // feat passes ValidName but is no agent
		"coder3/x":      "master", // a prefix that is not in {p}:agents
		"nobranch":      "master",
	} {
		if got := ConflictOwner(board, agents, branch, "master"); got != want {
			t.Errorf("%s: %q, want %q", branch, got, want)
		}
	}
	if got := ConflictOwner(board, map[string]bool{}, "coder/x", "boss"); got != "boss" {
		t.Errorf("no coder registered: %q", got)
	}
}

// §2's budget: a pass whose time is spent leaves every candidate for the
// next pass, with a reason, judged neither clean nor conflicting.
func TestCheckConflictsOutOfTime(t *testing.T) {
	w := newConflictWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, why := CheckConflicts(ctx, ConflictCheck{Dir: w.work, Scratch: t.TempDir(), Env: w.env, Fetch: true}, w.mainTip, cands(w))
	if why != "" || len(got) != 2 {
		t.Fatalf("%v %q", got, why)
	}
	for k, j := range got {
		if j.Why != notReached || j.Conflict {
			t.Errorf("%s: %+v", k, j)
		}
	}
}

// foureyes: only merge-tree's standard output names files; a warning git
// writes on its error output is never one.
func TestCheckConflictsReadsStdoutOnly(t *testing.T) {
	fake := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"*merge-tree\\ -h*) echo \"usage: git merge-tree [--write-tree] <b1> <b2>\"; exit 129;;\n" +
		"*merge-tree*) echo 4b825dc642cb6eb9a060e54bf8d69288fbee4904; echo f; echo \"warning: in the working copy\" >&2; exit 1;;\n" +
		"*hash-object*) echo 4b825dc642cb6eb9a060e54bf8d69288fbee4904;;\n" +
		"*init*) /bin/mkdir -p \"$5/objects/info\";;\n" +
		"*rev-parse*) echo \"$PWD/.git\";;\n" +
		"esac\nexit 0\n"
	os.WriteFile(filepath.Join(fake, "git"), []byte(script), 0o755)
	got, why := CheckConflicts(context.Background(), ConflictCheck{Dir: t.TempDir(), Scratch: t.TempDir(), Env: []string{"PATH=" + fake}}, strings.Repeat("a", 40),
		[]Candidate{{Key: "x", Branch: "x", Head: strings.Repeat("b", 40)}})
	if j := got["x"]; why != "" || !j.Conflict || !slices.Equal(j.Files, []string{"f"}) {
		t.Fatalf("%+v %q", j, why)
	}
}
