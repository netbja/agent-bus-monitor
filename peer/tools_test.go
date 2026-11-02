package peer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// shippedDenies is the shipped manifest's effective Read denies for a role,
// exactly as the launch merges them.
func shippedDenies(t *testing.T) []string {
	t.Helper()
	m, err := rolecfg.Load(filepath.Join("..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ReadDenies(m, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if len(d) < 15 {
		t.Fatalf("only %d Read denies: %v", len(d), d)
	}
	return d
}

// samplePath turns a deny pattern into a path it must refuse: ** becomes two
// directories, * a word, ~ the home.
func samplePath(pattern, root, home string) string {
	p := strings.ReplaceAll(pattern, "**", "a/b")
	p = strings.ReplaceAll(p, "*", "x")
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return filepath.Join(root, p)
}

// Every shipped Read deny refuses a path built from it, with Claude Code's
// glob meaning (** any depth, * one segment, ~ the home); near misses pass.
func TestDenyMatcherCoversTheShippedRules(t *testing.T) {
	root, home := "/work/proj", "/home/u"
	m, err := NewDenyMatcher(shippedDenies(t), root, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range shippedDenies(t) {
		if p := samplePath(d, root, home); !m.Denied(p) {
			t.Errorf("Read(%s) does not refuse %s", d, p)
		}
	}
	for _, p := range []string{"/work/proj/.env", "/work/proj/deep/er/.env.local", "/work/proj/tls/server.pem", "/home/u/.ssh/config", "/home/u/.ssh/keys/a", "/work/proj/id_rsa.pub"} {
		if !m.Denied(p) {
			t.Errorf("%s not refused", p)
		}
	}
	for _, p := range []string{"/work/proj/.env.example", "/work/proj/README.md", "/work/proj/keys.go", "/work/proj/pem.go", "/home/u/.sshrc"} {
		if m.Denied(p) {
			t.Errorf("%s refused, a near miss", p)
		}
	}
}

func TestDenyPatternForms(t *testing.T) {
	m, err := NewDenyMatcher([]string{"./secrets/*.json", "/build/out/**", "//etc/shadow", "docs/private.md"}, "/r", "/h")
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		"/r/secrets/a.json": true, "/r/secrets/sub/a.json": false, "/r/x/secrets/a.json": false,
		"/r/build/out/x/y": true, "/build/out/x": false, "/etc/shadow": true,
		"/r/docs/private.md": true, "/r/a/docs/private.md": false,
	} {
		if m.Denied(p) != want {
			t.Errorf("%s denied = %v, want %v", p, !want, want)
		}
	}
	// Fails closed: a rule it cannot read stops the peer.
	for _, bad := range []string{"", "[", "a/[x"} {
		if _, err := NewDenyMatcher([]string{bad}, "/r", "/h"); err == nil {
			t.Errorf("pattern %q accepted", bad)
		}
	}
}

func TestReadDeniesFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"p\"\n[profiles.p]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m\"\n" +
		"[tool_rules]\ndeny = [\"Read(**/.env)\", \"Read(**/broken\"]\n[roles.local]\ntier = \"pop\"\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		return // the manifest itself is refused: closed either way
	}
	if _, err := ReadDenies(m, "local"); err == nil {
		t.Fatal("a malformed Read rule was accepted")
	}
	if _, err := ReadDenies(m, "nobody"); err == nil {
		t.Fatal("an unknown role was accepted")
	}
}

// toolsFixture is a project root with a secret, a key, a binary, .git, and
// symlinks out of the root and onto a denied file.
func toolsFixture(t *testing.T) Tools {
	t.Helper()
	base := t.TempDir()
	root, home, outside := filepath.Join(base, "proj"), filepath.Join(base, "home"), filepath.Join(base, "outside")
	for _, d := range []string{root + "/sub", root + "/.git", home + "/.ssh", outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		root + "/main.go": "package main\n\nfunc main() { board() }\n", root + "/sub/b.txt": "the board holds tasks\n",
		root + "/.env": "TOKEN=hunter2\n", root + "/tls.pem": "-----BEGIN-----\nboard\n", root + "/.git/config": "board in git\n",
		root + "/bin.dat": "board\x00\x01\x02", outside + "/secret.txt": "board outside\n", home + "/.ssh/id_rsa": "KEY",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Symlink(outside+"/secret.txt", root+"/out.txt")
	_ = os.Symlink(root+"/.env", root+"/innocent.txt")
	deny, err := NewDenyMatcher(shippedDenies(t), root, home)
	if err != nil {
		t.Fatal(err)
	}
	return Tools{Root: root, Deny: deny,
		Thread: func(context.Context, string) ([]bus.Event, error) {
			return []bus.Event{{ID: "1-0", From: "master", Target: "local", Type: "directive", Message: "what does board.go hold?"}}, nil
		},
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) {
			return map[string]bus.AgentSnapshot{"master": {State: "idle", Message: "waiting"}, "local": {State: "working"}}, nil
		}}
}

func run(t *testing.T, tl Tools, name string, args map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(args)
	return tl.Run(context.Background(), name, string(b))
}

func TestToolsStayInTheRootAndOffDenies(t *testing.T) {
	tl := toolsFixture(t)
	if out := run(t, tl, "read_file", map[string]any{"path": "sub/b.txt"}); out != "the board holds tasks\n" {
		t.Fatalf("read_file = %q", out)
	}
	for _, p := range []string{"../outside/secret.txt", "/etc/passwd", "sub/../../outside/secret.txt", "out.txt", ".env", "innocent.txt", "tls.pem", "../home/.ssh/id_rsa"} {
		out := run(t, tl, "read_file", map[string]any{"path": p})
		if !strings.HasPrefix(out, "refused:") || strings.Contains(out, "hunter2") || strings.Contains(out, "outside\n") || strings.Contains(out, "KEY") {
			t.Errorf("read_file(%s) = %q, want refused", p, out)
		}
	}
	if out := run(t, tl, "read_file", map[string]any{"path": "main.go", "offset": 14, "limit": 4}); out != "func" {
		t.Fatalf("offset/limit = %q", out)
	}
	out := run(t, tl, "list_dir", map[string]any{"path": ""})
	for _, want := range []string{"main.go", "sub/"} {
		if !strings.Contains(out, want) {
			t.Errorf("list_dir lacks %s: %q", want, out)
		}
	}
	if out := run(t, tl, "list_dir", map[string]any{"path": ".."}); !strings.HasPrefix(out, "refused:") {
		t.Errorf("list_dir(..) = %q", out)
	}
	out = run(t, tl, "grep", map[string]any{"pattern": "board"})
	if !strings.Contains(out, "main.go:3:") || !strings.Contains(out, "sub/b.txt:1:") {
		t.Errorf("grep misses matches: %q", out)
	}
	for _, leak := range []string{".env", "tls.pem", ".git", "bin.dat", "outside", "innocent"} {
		if strings.Contains(out, leak) {
			t.Errorf("grep reached %s: %q", leak, out)
		}
	}
	if out := run(t, tl, "grep", map[string]any{"pattern": "("}); !strings.HasPrefix(out, "error:") {
		t.Errorf("bad regexp = %q", out)
	}
}

func TestToolsLimitsAndBusTools(t *testing.T) {
	tl := toolsFixture(t)
	big := strings.Repeat("board line\n", 10000) // 110 KB
	if err := os.WriteFile(filepath.Join(tl.Root, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := run(t, tl, "read_file", map[string]any{"path": "big.txt"}); len(out) > 64<<10+200 || !strings.Contains(out, "truncated") {
		t.Errorf("read_file of 110 KB = %d bytes", len(out))
	}
	if out := run(t, tl, "grep", map[string]any{"pattern": "board", "path": "big.txt"}); strings.Count(out, "\n") > 201 || !strings.Contains(out, "200 matches") {
		t.Errorf("grep cap: %d lines", strings.Count(out, "\n"))
	}
	for i := 0; i < 520; i++ {
		_ = os.WriteFile(filepath.Join(tl.Root, "sub", "f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26)), nil, 0o644)
	}
	if out := run(t, tl, "list_dir", map[string]any{"path": "sub"}); strings.Count(out, "\n") > 501 {
		t.Errorf("list_dir cap: %d lines", strings.Count(out, "\n"))
	}
	if out := run(t, tl, "bus_thread", map[string]any{"id": "1-0"}); !strings.Contains(out, "master") || !strings.Contains(out, "what does board.go hold?") {
		t.Errorf("bus_thread = %q", out)
	}
	if out := run(t, tl, "bus_agents", map[string]any{}); !strings.Contains(out, "master idle") || !strings.Contains(out, "local working") {
		t.Errorf("bus_agents = %q", out)
	}
	for name, args := range map[string]string{"rm_rf": `{}`, "read_file": `{"path":`, "bus_thread": `{"id":""}`} {
		if out := tl.Run(context.Background(), name, args); !strings.HasPrefix(out, "error:") {
			t.Errorf("%s(%s) = %q, want an error for the model", name, args, out)
		}
	}
}

// A named pipe (or any non-regular file) is refused before it is opened:
// opening one blocks until a writer comes, past the turn's clock, with the
// receiver lease held. grep skips it too.
func TestToolsRefuseSpecialFiles(t *testing.T) {
	tl := toolsFixture(t)
	if err := syscall.Mkfifo(filepath.Join(tl.Root, "pipe"), 0o644); err != nil {
		t.Skip("no mkfifo:", err)
	}
	if err := os.Symlink("pipe", filepath.Join(tl.Root, "pipe-link")); err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	go func() {
		done <- []string{
			run(t, tl, "read_file", map[string]any{"path": "pipe"}),
			run(t, tl, "read_file", map[string]any{"path": "pipe-link"}),
			run(t, tl, "grep", map[string]any{"pattern": "board"}),
			run(t, tl, "grep", map[string]any{"pattern": "board", "path": "pipe"}),
		}
	}()
	select {
	case out := <-done:
		for i, o := range out[:2] {
			if o != "error: not a regular file" {
				t.Errorf("read %d = %q, want not a regular file", i, o)
			}
		}
		if !strings.Contains(out[2], "main.go:3:") {
			t.Errorf("grep over a root with a pipe = %q", out[2])
		}
		if strings.Contains(out[3], "board") && !strings.HasPrefix(out[3], "no match") {
			t.Errorf("grep on the pipe = %q", out[3])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a tool blocked on a named pipe")
	}
}

// A denied name is refused even when it is a link to a file no rule names
// (.env -> config/shared-vals); a dangling link's error never names its target.
func TestToolsDenyTheAskedName(t *testing.T) {
	tl := toolsFixture(t)
	if err := os.MkdirAll(filepath.Join(tl.Root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tl.Root, "config", "shared-vals"), []byte("TOKEN=hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../config/shared-vals", filepath.Join(tl.Root, "sub", ".env")); err != nil {
		t.Fatal(err)
	}
	if out := run(t, tl, "read_file", map[string]any{"path": "sub/.env"}); !strings.HasPrefix(out, "refused:") {
		t.Fatalf("read_file(sub/.env -> config/shared-vals) = %q, want refused", out)
	}
	if out := run(t, tl, "grep", map[string]any{"pattern": "hunter2", "path": "sub"}); strings.Contains(out, "hunter2") {
		t.Fatalf("grep reached it: %q", out)
	}
	if err := os.Symlink("/very/secret/place", filepath.Join(tl.Root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if out := run(t, tl, "read_file", map[string]any{"path": "dangling"}); strings.Contains(out, "secret/place") {
		t.Fatalf("the dangling link's target leaks: %q", out)
	}
}

// A bare Read deny (or Read(**)) denies every read: the peer fails closed.
// A pattern without a slash matches at any depth, as gitignore and Claude Code
// read it.
func TestDenyAllAndBareNames(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"p\"\n[profiles.p]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m\"\n" +
		"[roles.local]\ntier = \"pop\"\n[roles.local.tool_rules]\ndeny = [\"Read\"]\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ReadDenies(m, "local")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := NewDenyMatcher(d, "/r", "/h")
	if err != nil || !dm.Denied("/r/README.md") || !dm.Denied("/r/a/b/c.go") {
		t.Fatalf("a bare Read deny = %v %v, want every read denied", d, err)
	}
	dm, err = NewDenyMatcher([]string{"*.pem", "secrets.txt"}, "/r", "/h")
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{"/r/a.pem": true, "/r/deep/b.pem": true, "/r/x/secrets.txt": true, "/r/pem.go": false} {
		if dm.Denied(p) != want {
			t.Errorf("%s denied = %v, want %v", p, !want, want)
		}
	}
}

// grep cuts a long matching line: one minified line never fills a result.
func TestGrepCutsLongLines(t *testing.T) {
	tl := toolsFixture(t)
	if err := os.WriteFile(filepath.Join(tl.Root, "min.js"), []byte("board"+strings.Repeat("x", 100000)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := run(t, tl, "grep", map[string]any{"pattern": "board", "path": "min.js"}); len(out) > 400 {
		t.Fatalf("one line gave %d bytes", len(out))
	}
}

// A home reached through a symlink (/home -> /var/home on Fedora Atomic) still
// has its ~/ denies matched: the tools compare resolved paths, so the matcher
// resolves the home too (SEC-LLM-6).
func TestDenyMatcherResolvesTheHome(t *testing.T) {
	base := t.TempDir()
	real, link := filepath.Join(base, "var", "home", "u"), filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(real, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "var", "home"), link); err != nil {
		t.Fatal(err)
	}
	m, err := NewDenyMatcher([]string{"~/.ssh/**"}, base, filepath.Join(link, "u"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(real, ".ssh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(resolved, "id_ed25519"), filepath.Join(link, "u", ".ssh", "id_ed25519")} {
		if !m.Denied(p) {
			t.Errorf("%s is not denied", p)
		}
	}
}

// The repository's .git is never read: its config can hold a remote URL with
// a token, sent off-host with a remote endpoint (SEC-LLM-4). A link onto it
// is refused as well; grep already skipped it.
func TestToolsRefuseTheGitDirectory(t *testing.T) {
	tl := toolsFixture(t)
	if err := os.Symlink(".git/config", filepath.Join(tl.Root, "gitcfg")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ tool, path string }{
		{"read_file", ".git/config"}, {"read_file", "gitcfg"}, {"list_dir", ".git"}, {"grep", ".git"}, {"read_file", "sub/../.git/config"},
	} {
		args := map[string]any{"path": c.path}
		if c.tool == "grep" {
			args["pattern"] = "board"
		}
		if out := run(t, tl, c.tool, args); !strings.HasPrefix(out, "refused:") {
			t.Errorf("%s(%s) = %q, want refused", c.tool, c.path, out)
		}
	}
	if out := run(t, tl, "list_dir", map[string]any{"path": ""}); !strings.Contains(out, "main.go") {
		t.Fatalf("the root no longer lists: %q", out)
	}
}

// grep stops with the turn's clock and after a bounded number of entries, so
// a large root (node_modules, .worktrees) never holds the receiver lease past
// turn_timeout (SEC-LLM-5); each stop is said.
func TestGrepStopsWithTheTurnAndAtItsEntryCap(t *testing.T) {
	tl := toolsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, _ := json.Marshal(map[string]any{"pattern": "board"})
	if out := tl.Run(ctx, "grep", string(b)); !strings.Contains(out, "[stopped: the turn ended]") || strings.Contains(out, "main.go:3:") {
		t.Fatalf("grep after the turn ended = %q", out)
	}
	defer func(n int) { grepEntries = n }(grepEntries)
	grepEntries = 3
	if out := run(t, tl, "grep", map[string]any{"pattern": "zzz-none"}); !strings.Contains(out, "[stopped after 3 entries]") {
		t.Fatalf("grep past its entry cap = %q", out)
	}
}

// A file or a directory swapped for a link between the checks and the open
// (another local writer) is refused, never followed: the open walks the
// checked path from the root without following any link (R1).
func TestToolsRefuseAPathSwappedForALink(t *testing.T) {
	defer func() { beforeOpen = nil }()
	for _, c := range []struct{ name, path, swap, target, leak string }{
		{"a file onto a file outside", "sub/b.txt", "sub/b.txt", "outside/secret.txt", "board outside"},
		{"a file onto a denied one", "sub/b.txt", "sub/b.txt", "proj/.env", "hunter2"},
		{"a directory onto one outside", "sub/b.txt", "sub", "outside", "board outside"},
	} {
		tl := toolsFixture(t)
		base := filepath.Dir(tl.Root)
		if err := os.WriteFile(filepath.Join(base, "outside", "b.txt"), []byte("board outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		beforeOpen = func() {
			p := filepath.Join(tl.Root, c.swap)
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(base, c.target), p); err != nil {
				t.Fatal(err)
			}
		}
		out := run(t, tl, "read_file", map[string]any{"path": c.path})
		if strings.Contains(out, c.leak) || !strings.HasPrefix(out, "refused:") {
			t.Errorf("%s: read_file = %q, want refused", c.name, out)
		}
	}
	// list_dir too: a directory swapped for a link lists nothing outside.
	tl := toolsFixture(t)
	beforeOpen = func() {
		p := filepath.Join(tl.Root, "sub")
		_ = os.RemoveAll(p)
		_ = os.Symlink(filepath.Join(filepath.Dir(tl.Root), "outside"), p)
	}
	if out := run(t, tl, "list_dir", map[string]any{"path": "sub"}); strings.Contains(out, "secret.txt") || !strings.HasPrefix(out, "refused:") {
		t.Errorf("list_dir after the swap = %q, want refused", out)
	}
}

// One directory larger than the caps is read in bounded batches: grep stops
// reading it at its entry cap, list_dir past its 500 names, instead of loading
// every name first (reviewer-codex: WalkDir and ReadDir(-1) read a whole
// directory before any check ran).
func TestToolsReadAHugeDirectoryInBatches(t *testing.T) {
	tl := toolsFixture(t)
	huge := filepath.Join(tl.Root, "huge")
	if err := os.MkdirAll(huge, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		if err := os.WriteFile(filepath.Join(huge, "f"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := 0
	dirBatchRead = func(n int) { read += n }
	defer func(n int) { dirBatchRead, grepEntries = nil, n }(grepEntries)
	grepEntries = 10
	if out := run(t, tl, "grep", map[string]any{"pattern": "x", "path": "huge"}); !strings.Contains(out, "[stopped after 10 entries]") {
		t.Fatalf("grep = %q", out)
	}
	if read > grepEntries+dirBatch {
		t.Fatalf("grep read %d names of a 3000-entry directory to stop at %d", read, grepEntries)
	}
	read = 0
	out := run(t, tl, "list_dir", map[string]any{"path": "huge"})
	if read > listCap+1 || strings.Count(out, "\n") != listCap+1 || !strings.Contains(out, "more not listed") {
		t.Fatalf("list_dir read %d names and printed %d lines", read, strings.Count(out, "\n"))
	}
}

// A ~/ deny with no absolute home to anchor it is an error, never a pattern
// relative to the process's folder that matches nothing (reviewer-codex).
func TestDenyMatcherNeedsAnAbsoluteHome(t *testing.T) {
	for _, home := range []string{"", "rel"} {
		if _, err := NewDenyMatcher([]string{"~/.ssh/**"}, "/work/proj", home); err == nil {
			t.Errorf("home %q: no error", home)
		}
	}
	if _, err := NewDenyMatcher([]string{"*.pem"}, "/work/proj", ""); err != nil {
		t.Errorf("a pattern that needs no home: %v", err)
	}
}

// A named pipe under the root is refused by its type before any open: no
// tool opens it, not even non-blocking (foureyes).
func TestToolsNeverOpenAPipe(t *testing.T) {
	tl := toolsFixture(t)
	pipe := filepath.Join(tl.Root, "sub", "pipe")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skip("no mkfifo:", err)
	}
	var seen []string
	opened = func(p string) { seen = append(seen, p) }
	defer func() { opened = nil }()
	if out := run(t, tl, "read_file", map[string]any{"path": "sub/pipe"}); out != "error: not a regular file" {
		t.Fatalf("read_file = %q", out)
	}
	run(t, tl, "grep", map[string]any{"pattern": "board", "path": "sub"})
	run(t, tl, "list_dir", map[string]any{"path": "sub/pipe"})
	for _, p := range seen {
		if strings.HasSuffix(p, "pipe") {
			t.Fatalf("the pipe was opened: %v", seen)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the hook saw no open at all")
	}
}

// A link inside .git onto a file outside it is refused by its asked path:
// the inGit check on the asked path alone (foureyes).
func TestToolsRefuseTheAskedGitPath(t *testing.T) {
	tl := toolsFixture(t)
	if err := os.Symlink("../sub/b.txt", filepath.Join(tl.Root, ".git", "link")); err != nil {
		t.Fatal(err)
	}
	if out := run(t, tl, "read_file", map[string]any{"path": ".git/link"}); !strings.HasPrefix(out, "refused:") {
		t.Fatalf("read_file(.git/link -> sub/b.txt) = %q", out)
	}
}

// grep's walk opens each directory beneath the root (R1), as the hook sees,
// never by its name (foureyes: os.Open in its place passed every test).
func TestGrepOpensDirectoriesBeneathTheRoot(t *testing.T) {
	tl := toolsFixture(t)
	var seen []string
	opened = func(p string) { seen = append(seen, p) }
	defer func() { opened = nil }()
	run(t, tl, "grep", map[string]any{"pattern": "board"})
	for _, d := range []string{tl.Root, filepath.Join(tl.Root, "sub")} {
		if !slices.Contains(seen, d) {
			t.Fatalf("%s not opened beneath the root: %v", d, seen)
		}
	}
}
