package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Claude Code keys trust on the git root; a worktree uses its main
// checkout's root; outside a repository, the directory itself.
func TestTrustKey(t *testing.T) {
	root := t.TempDir()
	repo := mkdir(t, root, "repo")
	mkdir(t, repo, ".git", "worktrees", "wt")
	sub := mkdir(t, repo, "cmd", "x")
	wt := mkdir(t, root, "repo-wt")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n")
	rel := mkdir(t, root, "repo-rel") // git worktree add --relative-paths
	write(t, filepath.Join(rel, ".git"), "gitdir: ../repo/.git/worktrees/rel\n")
	plain := mkdir(t, root, "plain", "deep")
	cases := map[string]struct {
		key    string
		inRepo bool
	}{repo: {repo, true}, sub: {repo, true}, wt: {repo, true}, rel: {repo, true}, plain: {plain, false}}
	for dir, want := range cases {
		key, inRepo := Key(dir)
		if key != want.key || inRepo != want.inRepo {
			t.Errorf("Key(%s) = %s %v, want %s %v", dir, key, inRepo, want.key, want.inRepo)
		}
	}
}

func TestClaudeTrusted(t *testing.T) {
	root := t.TempDir()
	repo := mkdir(t, root, "repo")
	mkdir(t, repo, ".git")
	other := mkdir(t, root, "other")
	mkdir(t, other, ".git")
	plain := mkdir(t, root, "plain", "deep")
	file := write(t, filepath.Join(root, "claude.json"), `{"oauthAccount":{"emailAddress":"x@y"},"primaryApiKey":"sk-secret",
	 "projects":{"`+repo+`":{"hasTrustDialogAccepted":true,"lastCost":3},
	             "`+other+`":{"hasTrustDialogAccepted":false},
	             "`+filepath.Join(root, "plain")+`":{"hasTrustDialogAccepted":true}}}`)
	cases := map[string]bool{repo: true, filepath.Join(repo): true, other: false, plain: true, mkdir(t, root, "none"): false}
	for dir, want := range cases {
		if got := ClaudeTrusted(file, dir); got != want {
			t.Errorf("ClaudeTrusted(%s) = %v, want %v", dir, got, want)
		}
	}
	// A nested repository is not covered by a trusted parent directory.
	nested := mkdir(t, root, "plain", "clone")
	mkdir(t, nested, ".git")
	if ClaudeTrusted(file, nested) {
		t.Error("a repository inside a trusted directory read as trusted")
	}
	if ClaudeTrusted(filepath.Join(root, "missing.json"), repo) {
		t.Error("a missing file read as trusted")
	}
	bad := write(t, filepath.Join(root, "bad.json"), `{"projects": {"`+repo+`": {"hasTrustDialogAc`) // Claude mid-rewrite
	if ClaudeTrusted(bad, repo) {
		t.Error("a half-written file read as trusted")
	}
}

func TestCodexTrusted(t *testing.T) {
	root := t.TempDir()
	repo := mkdir(t, root, "repo")
	mkdir(t, repo, ".git")
	sub := mkdir(t, repo, "sub")
	untrusted := mkdir(t, root, "u")
	file := write(t, filepath.Join(root, "config.toml"), "model = \"x\"\n[projects.\""+repo+"\"]\ntrust_level = \"trusted\"\n[projects.\""+untrusted+"\"]\ntrust_level = \"untrusted\"\n")
	for dir, want := range map[string]bool{repo: true, sub: true, untrusted: false, mkdir(t, root, "n"): false} {
		if got := CodexTrusted(file, dir); got != want {
			t.Errorf("CodexTrusted(%s) = %v, want %v", dir, got, want)
		}
	}
	if CodexTrusted(filepath.Join(root, "missing.toml"), repo) {
		t.Error("a missing file read as trusted")
	}
}

// Reader reads each client's own file: Claude Code's $CLAUDE_CONFIG_DIR/
// .claude.json, else ~/.claude.json; Codex's $CODEX_HOME/config.toml, else
// ~/.codex/config.toml. A client that is not gated is trusted.
func TestReaderResolvesEachClientsFile(t *testing.T) {
	root := t.TempDir()
	home, alt := mkdir(t, root, "home"), mkdir(t, root, "alt")
	dir := mkdir(t, root, "work")
	accepted := `{"projects":{"` + dir + `":{"hasTrustDialogAccepted":true}}}`
	trusted := "[projects.\"" + dir + "\"]\ntrust_level = \"trusted\"\n"
	cases := []struct {
		name   string
		env    map[string]string
		files  map[string]string
		client string
		want   bool
	}{
		{"claude in home", map[string]string{"HOME": home}, map[string]string{filepath.Join(home, ".claude.json"): accepted}, "claude-code", true},
		{"claude config dir wins", map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": alt}, map[string]string{filepath.Join(home, ".claude.json"): accepted}, "claude-code", false},
		{"claude config dir", map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": alt}, map[string]string{filepath.Join(alt, ".claude.json"): accepted}, "claude-code", true},
		{"codex in home", map[string]string{"HOME": home}, map[string]string{filepath.Join(home, ".codex", "config.toml"): trusted}, "codex", true},
		{"codex home wins", map[string]string{"HOME": home, "CODEX_HOME": alt}, map[string]string{filepath.Join(home, ".codex", "config.toml"): trusted}, "codex", false},
		{"codex home", map[string]string{"HOME": home, "CODEX_HOME": alt}, map[string]string{filepath.Join(alt, "config.toml"): trusted}, "codex", true},
		{"not gated", map[string]string{"HOME": home}, nil, "kimi-cli", true},
	}
	for _, c := range cases {
		for _, p := range []string{filepath.Join(home, ".claude.json"), filepath.Join(alt, ".claude.json"), filepath.Join(home, ".codex", "config.toml"), filepath.Join(alt, "config.toml")} {
			os.Remove(p)
		}
		for p, body := range c.files {
			mkdir(t, filepath.Dir(p))
			write(t, p, body)
		}
		if got := Reader(func(k string) string { return c.env[k] })(c.client, dir); got != c.want {
			t.Errorf("%s: Reader(%s) = %v, want %v", c.name, c.client, got, c.want)
		}
	}
}
