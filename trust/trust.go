// Package trust reads a client's "do you trust this folder?" answer
// (first-run-trust). It only ever reads the clients' files, and only the
// trust flag: the readers return a bool, never content, so nothing of these
// files (which may hold account data or an API key) can reach a log or an
// error. agentbus up and start gate on it; busmon says what to choose.
package trust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Key is where Claude Code keys a folder's trust: the git repository's
// root (for a worktree, its main checkout's root), else the folder itself.
func Key(dir string) (key string, inRepo bool) {
	dir = filepath.Clean(dir)
	for d := dir; ; d = filepath.Dir(d) {
		st, err := os.Lstat(filepath.Join(d, ".git"))
		switch {
		case err == nil && st.IsDir():
			return d, true
		case err == nil && st.Mode().IsRegular():
			b, err := os.ReadFile(filepath.Join(d, ".git"))
			gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
			if gitdir != "" && !filepath.IsAbs(gitdir) { // git worktree add --relative-paths
				gitdir = filepath.Join(d, gitdir)
			}
			if main, _, ok := strings.Cut(filepath.ToSlash(gitdir), "/.git/worktrees/"); err == nil && ok {
				return filepath.FromSlash(main), true
			}
			return d, true
		}
		if parent := filepath.Dir(d); parent == d {
			return dir, false
		}
	}
}

// ClaudeTrusted reads only projects[<key>].hasTrustDialogAccepted from
// ~/.claude.json. Outside a repository a trusted ancestor folder covers the
// folder, as Claude Code documents; a repository is covered only by its own
// root's entry. Any error (missing, unreadable, half-written while Claude
// rewrites it) is "not trusted".
func ClaudeTrusted(path, dir string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	defer clear(b)
	var f struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &f) != nil {
		return false
	}
	key, inRepo := Key(dir)
	if f.Projects[key].Accepted {
		return true
	}
	if inRepo {
		return false
	}
	for d := filepath.Dir(key); d != filepath.Dir(d); d = filepath.Dir(d) {
		if f.Projects[d].Accepted {
			return true
		}
	}
	return false
}

// CodexTrusted reads only [projects."<path>"].trust_level from Codex's
// config.toml: trusted when the repository's root, or the folder itself, is
// "trusted". Any error is "not trusted".
func CodexTrusted(path, dir string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	defer clear(b)
	var f struct {
		Projects map[string]struct {
			Trust string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if _, err := toml.Decode(string(b), &f); err != nil {
		return false
	}
	key, _ := Key(dir)
	return f.Projects[key].Trust == "trusted" || f.Projects[filepath.Clean(dir)].Trust == "trusted"
}

// Gated are the clients whose folder trust gates a start.
var Gated = map[string]bool{"claude-code": true, "codex": true}

// Label is the client's name as the person knows it.
func Label(client string) string {
	switch client {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return client
}

// Reader reads the trust of dir by client from its own file: Claude
// Code's ~/.claude.json (or $CLAUDE_CONFIG_DIR/.claude.json), Codex's
// $CODEX_HOME/config.toml (default ~/.codex). Other clients are not gated.
func Reader(env func(string) string) func(client, dir string) bool {
	return func(client, dir string) bool {
		switch client {
		case "claude-code":
			base := env("CLAUDE_CONFIG_DIR")
			if base == "" {
				base = env("HOME")
			}
			return ClaudeTrusted(filepath.Join(base, ".claude.json"), dir)
		case "codex":
			base := env("CODEX_HOME")
			if base == "" {
				base = filepath.Join(env("HOME"), ".codex")
			}
			return CodexTrusted(filepath.Join(base, "config.toml"), dir)
		}
		return true
	}
}
