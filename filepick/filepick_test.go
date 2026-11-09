package filepick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// write creates root/rel with body, and its directories.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// The ignore rules, read as text (§2.1): patterns, ! negation, a trailing /
// for directories only, a leading / to anchor, **, and a pattern without a
// slash at any depth; a sub-directory's .gitignore is relative to it.
func TestIgnoreRules(t *testing.T) {
	var s ignoreSet
	s = s.with("", "# a comment\n\n*.log\n!keep.log\n/build/\ntmp/\ndocs/**/gen\nlogs/**\n\\#hash\nspace.txt   \n")
	s = s.with("sub", "local.txt\n/only-here\n")
	for _, c := range []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"a.log", false, true}, {"deep/x/a.log", false, true}, {"keep.log", false, false}, {"deep/keep.log", false, false},
		{"build", true, true}, {"build", false, false}, {"sub/build", true, false}, // anchored, directories only
		{"tmp", true, true}, {"x/tmp", true, true}, {"tmp", false, false},
		{"docs/gen", false, true}, {"docs/a/b/gen", false, true}, {"other/gen", false, false},
		{"logs/a", false, true}, {"logs", true, false}, // logs/** is what is inside, not logs itself
		{"#hash", false, true}, {"space.txt", false, true},
		{"sub/local.txt", false, true}, {"sub/x/local.txt", false, true}, {"local.txt", false, false},
		{"sub/only-here", false, true}, {"sub/x/only-here", false, false},
		{"README.md", false, false},
	} {
		if got := s.ignored(c.rel, c.dir); got != c.want {
			t.Errorf("ignored(%q, dir=%v) = %v, want %v", c.rel, c.dir, got, c.want)
		}
	}
}

// fixture is a project root with ignore rules, skipped directories, secrets,
// a denied file, a worktree, a hostile git config and symlinks of every
// kind; outside is a directory beside it.
func fixture(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root, outside = filepath.Join(base, "proj"), filepath.Join(base, "outside")
	for rel, body := range map[string]string{
		".gitignore":        "*.log\n!keep.log\n/build/\n",
		".git/info/exclude": "excluded.txt\n",
		"README.md":         "r", "a.log": "", "keep.log": "", "excluded.txt": "", "my notes.md": "", "we`ird.md": "",
		"build/out.bin": "", "sub/build/x.go": "", "sub/.gitignore": "local.txt\n", "sub/local.txt": "", "local.txt": "",
		"node_modules/x.js": "", ".venv/bin/python": "", "vendor/v.go": "",
		".env": "SECRET=1", "certs/x.pem": "", ".ssh/config": "", "secrets/a.txt": "",
		".worktrees/w1/.git": "gitdir: /elsewhere\n", ".worktrees/w1/main.go": "", ".worktrees/w1/.gitignore": "*.tmp\n", ".worktrees/w1/x.tmp": "",
		"docs/a.md": "", "deep/1/2/3/f.txt": "",
	} {
		write(t, root, rel, body)
	}
	write(t, outside, "creds", "aws")
	link(t, filepath.Join(outside, "creds"), filepath.Join(root, "docs/credentials")) // outside the root
	link(t, filepath.Join(root, ".env"), filepath.Join(root, "docs/notes"))           // an in-root secret target
	link(t, filepath.Join(root, "README.md"), filepath.Join(root, "docs/alias.md"))   // fine
	link(t, filepath.Join(root, "gone.md"), filepath.Join(root, "docs/dangling"))
	link(t, root, filepath.Join(root, "docs/loop")) // a directory link: neither listed nor walked
	link(t, outside, filepath.Join(root, "docs/out"))
	link(t, filepath.Join(root, "secrets/a.txt"), filepath.Join(root, "docs/hidden.txt")) // a denied target
	return root, outside
}

func denySecrets(root string) func(string) bool {
	return func(abs string) bool { return strings.HasPrefix(abs, filepath.Join(root, "secrets")) }
}

func paths(es []Entry) []string {
	var out []string
	for _, e := range es {
		p := e.Path
		if e.Dir {
			p += "/"
		}
		if e.Repo {
			p += "(repo)"
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// §2 and §3: what the walk offers from the fixture, and nothing else.
func TestListWalk(t *testing.T) {
	root, _ := fixture(t)
	res, err := List(context.Background(), Options{Root: root, Deny: denySecrets(root)})
	if err != nil || res.Incomplete != "" {
		t.Fatalf("List: %v, incomplete %q", err, res.Incomplete)
	}
	want := []string{
		".gitignore", ".worktrees/", ".worktrees/w1/(repo)",
		"README.md", "certs/", "deep/", "deep/1/", "deep/1/2/", "deep/1/2/3/", "deep/1/2/3/f.txt",
		"docs/", "docs/a.md", "docs/alias.md", "keep.log", "local.txt", "my notes.md",
		"sub/", "sub/.gitignore", "sub/build/", "sub/build/x.go",
	}
	if got := paths(res.Entries); !slices.Equal(got, want) {
		t.Fatalf("listed:\n  %q\nwant:\n  %q", got, want)
	}
}

// §2.2: a worktree is one entry; a query under it walks inside it with its own
// rules, and the paths keep the prefix.
func TestListWorktreeOnDemand(t *testing.T) {
	root, outside := fixture(t)
	res, err := List(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	sub, ok := SubFor(res.Entries, ".worktrees/w1/ma")
	if !ok || sub != ".worktrees/w1" {
		t.Fatalf("SubFor = %q, %v", sub, ok)
	}
	if _, ok := SubFor(res.Entries, ".worktrees/w"); ok {
		t.Fatal("a query not under the worktree descended into it")
	}
	in, err := List(context.Background(), Options{Root: root, Sub: sub})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(in.Entries); !slices.Equal(got, []string{".worktrees/w1/.gitignore", ".worktrees/w1/main.go"}) {
		t.Fatalf("inside the worktree: %q", got)
	}
	write(t, outside, ".git/HEAD", "ref: x\n") // a repository beside the root: still outside
	if _, err := List(context.Background(), Options{Root: root, Sub: "../outside"}); err == nil {
		t.Fatal("a sub outside the root was walked")
	}
	if _, err := List(context.Background(), Options{Root: root, Sub: "docs"}); err == nil {
		t.Fatal("a sub that is not a repository entry was walked")
	}
}

// §2.1: the caps say why the list is incomplete and keep what was read.
func TestListBounds(t *testing.T) {
	root, _ := fixture(t)
	res, err := List(context.Background(), Options{Root: root, MaxDepth: 2})
	if err != nil || !strings.Contains(res.Incomplete, "deeper than 2") {
		t.Fatalf("depth: %v %q", err, res.Incomplete)
	}
	if slices.Contains(paths(res.Entries), "deep/1/2/") || !slices.Contains(paths(res.Entries), "deep/1/") { // two levels, not three
		t.Fatalf("depth 2: %q", paths(res.Entries))
	}
	res, err = List(context.Background(), Options{Root: root, MaxEntries: 3})
	if err != nil || len(res.Entries) != 3 || !strings.Contains(res.Incomplete, "over 3 entries") {
		t.Fatalf("count: %v %d %q", err, len(res.Entries), res.Incomplete)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = List(ctx, Options{Root: root})
	if err != nil || !strings.Contains(res.Incomplete, "took over") {
		t.Fatalf("deadline: %v %q", err, res.Incomplete)
	}
	res, err = List(context.Background(), Options{Root: root, Timeout: time.Nanosecond})
	if err != nil || !strings.Contains(res.Incomplete, "took over") {
		t.Fatalf("timeout: %v %q", err, res.Incomplete)
	}
}

// §2.3: no checkout on this machine.
func TestListNoCheckout(t *testing.T) {
	_, err := List(context.Background(), Options{Root: filepath.Join(t.TempDir(), "missing")})
	if !errors.Is(err, ErrNoCheckout) {
		t.Fatalf("err = %v, want ErrNoCheckout", err)
	}
	if _, err := List(context.Background(), Options{Root: "relative/path"}); !errors.Is(err, ErrNoCheckout) {
		t.Fatalf("a relative root: %v", err)
	}
}

// §9 (foureyes): no program runs. The repository's config names a monitor
// and a hooks path that would leave a mark; listing never runs them.
func TestListRunsNoProgram(t *testing.T) {
	root, outside := fixture(t)
	mark := filepath.Join(outside, "ran")
	script := filepath.Join(outside, "evil.sh")
	write(t, outside, "evil.sh", "#!/bin/sh\ntouch "+mark+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".git/config", "[core]\n\tfsmonitor = "+script+"\n\thooksPath = "+outside+"\n")
	if _, err := List(context.Background(), Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("listing ran the repository's configured program")
	}
}

// §3: the reason an exact query names a file that is never offered.
func TestRefused(t *testing.T) {
	root, _ := fixture(t)
	deny := denySecrets(root)
	for rel, want := range map[string]string{
		".env": "environment file", "certs/x.pem": "private-key file", ".ssh/config": "SSH directory",
		"secrets/a.txt": "denied by roles.toml", "we`ird.md": "unusual name", "docs/notes": "environment file",
		"docs/credentials": "outside the project", "README.md": "", "docs/alias.md": "",
	} {
		if got := Refused(root, rel, deny); got != want {
			t.Errorf("Refused(%q) = %q, want %q", rel, got, want)
		}
	}
}

// §4.2: name prefix, segment prefix, substring, subsequence; ties to the
// shorter path, then alphabetical; a / matches from the path's start; an
// empty query lists the recent picks, then the top level.
func TestRank(t *testing.T) {
	es := []Entry{
		{Path: "cmd", Dir: true}, {Path: "cmd/busmon", Dir: true}, {Path: "cmd/busmon/tea_compose.go"},
		{Path: "cmd/busmon/compose_test.go"}, {Path: "docs/compose.md"}, {Path: "README.md"},
		{Path: "bus/stream.go"}, {Path: "cmd/busmon/keys.go"}, {Path: "Makefile"}, {Path: "tools/cmd/build.go"},
	}
	names := func(es []Entry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Path)
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct{ q, want string }{
		{"compose", "docs/compose.md cmd/busmon/compose_test.go cmd/busmon/tea_compose.go"},
		{"COMP", "docs/compose.md cmd/busmon/compose_test.go cmd/busmon/tea_compose.go"},
		{"bus", "cmd/busmon bus/stream.go cmd/busmon/keys.go cmd/busmon/tea_compose.go cmd/busmon/compose_test.go"},
		{"stm", "bus/stream.go cmd/busmon/tea_compose.go"}, // subsequences only
		{"cmd/bu", "cmd/busmon cmd/busmon/keys.go cmd/busmon/tea_compose.go cmd/busmon/compose_test.go"},
		{"zzz", ""},
		{"", "README.md cmd Makefile"},
	} {
		recent := []string(nil)
		if c.q == "" {
			recent = []string{"README.md", "gone.md"}
		}
		if got := names(Rank(es, c.q, recent)); got != c.want {
			t.Errorf("Rank(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

// foureyes P2: a Sub whose path goes through a directory link escapes the
// root; every component must be a real directory inside it, and pass the
// filters a listed entry passes.
func TestListSubCannotEscape(t *testing.T) {
	root, outside := fixture(t)
	write(t, outside, "repo/.git/HEAD", "ref: x\n")
	write(t, outside, "repo/outside.txt", "")
	link(t, outside, filepath.Join(root, "evil"))
	if res, err := List(context.Background(), Options{Root: root, Sub: "evil/repo"}); err == nil {
		t.Fatalf("a Sub through a directory link was walked: %q", paths(res.Entries))
	}
	write(t, root, "build/r/.git/HEAD", "ref: x\n") // under /build/, ignored by the root's .gitignore
	write(t, root, "build/r/a.go", "")
	if _, err := List(context.Background(), Options{Root: root, Sub: "build/r"}); err == nil {
		t.Fatal("a Sub under an ignored directory was walked")
	}
	write(t, root, "secrets/r/.git/HEAD", "ref: x\n")
	if _, err := List(context.Background(), Options{Root: root, Sub: "secrets/r", Deny: denySecrets(root)}); err == nil {
		t.Fatal("a Sub under a denied directory was walked")
	}
	if _, err := List(context.Background(), Options{Root: root, Sub: ".worktrees/w1"}); err != nil {
		t.Fatalf("the real worktree: %v", err)
	}
}

// foureyes P2/P3: a link's own path passes the filters too, not only its
// target: a link named .env, or one the ignore rules name, is not offered.
func TestListLinkOwnPath(t *testing.T) {
	root, _ := fixture(t)
	link(t, filepath.Join(root, "README.md"), filepath.Join(root, "docs/.env"))
	link(t, filepath.Join(root, "README.md"), filepath.Join(root, "docs/ign.log"))
	write(t, root, "bad\x01.md", "")
	res, err := List(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths(res.Entries) {
		if p == "docs/.env" || p == "docs/ign.log" || strings.ContainsRune(p, 1) {
			t.Errorf("offered %q", p)
		}
	}
	if got := Refused(root, "docs/.env", nil); got != "environment file" {
		t.Errorf("Refused(docs/.env) = %q", got)
	}
	if got := Refused(root, "bad\x01.md", nil); got != "unusual name" {
		t.Errorf("Refused(control character) = %q", got)
	}
}

// reviewer-codex 3, foureyes P3: a .gitignore that cannot be read in full (too
// large, a link, not a regular file) is never treated as absent: its
// directory is left out and the list says why (fail closed).
func TestListUnreadableIgnoreFailsClosed(t *testing.T) {
	root, outside := fixture(t)
	write(t, root, "big/.gitignore", strings.Repeat("#", maxIgnoreFile+1))
	write(t, root, "big/x.txt", "")
	res, err := List(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(res.Entries); slices.Contains(got, "big/x.txt") || !strings.Contains(res.Incomplete, "big/.gitignore") {
		t.Fatalf("a .gitignore over the size: %q, incomplete %q", got, res.Incomplete)
	}
	// a .gitignore that is a link is never followed, whatever it points at
	other := t.TempDir()
	write(t, outside, "rules", "# nothing ignored\n")
	link(t, filepath.Join(outside, "rules"), filepath.Join(other, "linked/.gitignore"))
	write(t, other, "linked/y.txt", "")
	res, err = List(context.Background(), Options{Root: other})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(res.Entries); slices.Contains(got, "linked/y.txt") || !strings.Contains(res.Incomplete, "linked/.gitignore") {
		t.Fatalf("a linked .gitignore: %q, incomplete %q", got, res.Incomplete)
	}
	write(t, root, ".git/info/exclude", strings.Repeat("#", maxIgnoreFile+1))
	res, err = List(context.Background(), Options{Root: root})
	if err != nil || len(res.Entries) != 0 || !strings.Contains(res.Incomplete, "exclude") {
		t.Fatalf("an unreadable info/exclude: %v %d entries %q", err, len(res.Entries), res.Incomplete)
	}
}

// reviewer-codex 1, foureyes P3: one big directory is read in batches, so the
// cap and the deadline hold inside it.
func TestListBigDirectoryBounded(t *testing.T) {
	root := t.TempDir()
	for i := range 700 {
		write(t, root, fmt.Sprintf("flat/f%03d", i), "")
	}
	res, err := List(context.Background(), Options{Root: root, MaxEntries: 300})
	if err != nil || len(res.Entries) != 300 || !strings.Contains(res.Incomplete, "over 300") {
		t.Fatalf("cap: %v %d %q", err, len(res.Entries), res.Incomplete)
	}
	calls := 0
	t0 := time.Now()
	nowFunc = func() time.Time { calls++; return t0.Add(time.Duration(calls) * time.Millisecond) }
	t.Cleanup(func() { nowFunc = time.Now })
	res, err = List(context.Background(), Options{Root: root, Timeout: 100 * time.Millisecond})
	if err != nil || !strings.Contains(res.Incomplete, "took over") || len(res.Entries) >= 200 {
		t.Fatalf("deadline inside one directory: %v %d entries %q", err, len(res.Entries), res.Incomplete)
	}
}

// reviewer-codex 2: ** patterns are matched in bounded time, however many.
func TestIgnoreManyStarsBounded(t *testing.T) {
	pat := strings.Repeat("**/a/", 40) + "b"
	s := ignoreSet{}.with("", pat+"\n")
	rel := strings.TrimSuffix(strings.Repeat("a/", 12), "/")
	start := time.Now()
	if s.ignored(rel, false) {
		t.Fatal("matched")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("one match took %s", d)
	}
}

// foureyes P3: a sibling directory never sees another's rules, even when the
// parent's slice has room to grow.
func TestIgnoreSiblingsDoNotShare(t *testing.T) {
	parent := ignoreSet{}.with("", "one\ntwo\nthree\n") // three rules: room for a fourth
	x := parent.with("x", "px\n")
	y := parent.with("y", "py\n")
	if !x.ignored("x/px", false) || x.ignored("y/py", false) || !y.ignored("y/py", false) {
		t.Fatalf("siblings share rules: x(x/px)=%v x(y/py)=%v y(y/py)=%v", x.ignored("x/px", false), x.ignored("y/py", false), y.ignored("y/py", false))
	}
}
