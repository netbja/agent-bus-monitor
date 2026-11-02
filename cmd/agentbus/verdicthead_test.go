package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo is a throwaway local repository with one commit; no remote.
func gitRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "one")
	return dir, run("rev-parse", "HEAD")
}

// verdict --head (forge-delivery): the full SHA of a commit the reviewer's
// checkout holds; a short, unknown or malformed one is refused.
func TestResolveVerdictHead(t *testing.T) {
	dir, head := gitRepo(t)
	if got, err := resolveVerdictHead(dir, head); err != nil || got != head {
		t.Fatalf("full head = %q, %v", got, err)
	}
	for name, h := range map[string]string{
		"short":   head[:7],
		"unknown": strings.Repeat("ab", 20),
		"ref":     "main",
		"upper":   strings.ToUpper(head),
		"empty":   "",
	} {
		if _, err := resolveVerdictHead(dir, h); err == nil {
			t.Errorf("%s %q accepted", name, h)
		}
	}
}
