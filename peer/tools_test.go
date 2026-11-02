package peer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
