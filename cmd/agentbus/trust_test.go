package main

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
		key, inRepo := trustKey(dir)
		if key != want.key || inRepo != want.inRepo {
			t.Errorf("trustKey(%s) = %s %v, want %s %v", dir, key, inRepo, want.key, want.inRepo)
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
		if got := claudeTrusted(file, dir); got != want {
			t.Errorf("claudeTrusted(%s) = %v, want %v", dir, got, want)
		}
	}
	// A nested repository is not covered by a trusted parent directory.
	nested := mkdir(t, root, "plain", "clone")
	mkdir(t, nested, ".git")
	if claudeTrusted(file, nested) {
		t.Error("a repository inside a trusted directory read as trusted")
	}
	if claudeTrusted(filepath.Join(root, "missing.json"), repo) {
		t.Error("a missing file read as trusted")
	}
	bad := write(t, filepath.Join(root, "bad.json"), `{"projects": {"`+repo+`": {"hasTrustDialogAc`) // Claude mid-rewrite
	if claudeTrusted(bad, repo) {
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
		if got := codexTrusted(file, dir); got != want {
			t.Errorf("codexTrusted(%s) = %v, want %v", dir, got, want)
		}
	}
	if codexTrusted(filepath.Join(root, "missing.toml"), repo) {
		t.Error("a missing file read as trusted")
	}
}

func TestTabClient(t *testing.T) {
	dir := t.TempDir()
	manifest := write(t, filepath.Join(dir, "roles.toml"), `schema_version = 2
[profiles.claude]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "manual" }
[profiles.review]
client = "codex"
provider = "openai"
model = "gpt-x"
[defaults]
profile = "claude"
[roles.master]
tier = "boot"
skills = ["agent-bus"]
[roles.foureyes]
profile = "review"
tier = "boot"
skills = ["agent-bus"]
`)
	env := func(string) string { return "" }
	for cmd, want := range map[string]string{
		"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch master demo":       "claude-code",
		"env 'ROLES_TOML=" + manifest + "' '/r/scripts/agent-launch' foureyes demo": "codex",
		"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch ghost demo":        "",
		"/r/busmon --project demo": "",
		"env ROLES_TOML=/nowhere/roles.toml /r/scripts/agent-launch master demo": "",
	} {
		if got := tabClient(cmd, env); got != want {
			t.Errorf("tabClient(%q) = %q, want %q", cmd, got, want)
		}
	}
}
