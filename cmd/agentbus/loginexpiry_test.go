package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/loginexpiry"
)

// The service reads the transcripts of Claude's config dir: CLAUDE_CONFIG_DIR
// when set, else ~/.claude; with neither home nor dir, nothing.
func TestServiceLoginExpiryReadsClaudesConfigDir(t *testing.T) {
	now := time.Now()
	line := `{"type":"assistant","timestamp":"` + now.Add(-time.Minute).UTC().Format(time.RFC3339Nano) +
		`","isApiErrorMessage":true,"error":"authentication_failed","message":{"model":"<synthetic>"}}` + "\n"
	write := func(dir string) {
		p := filepath.Join(dir, "projects", "x")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "s.jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, alt := t.TempDir(), t.TempDir()
	write(filepath.Join(home, ".claude"))
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, ok := serviceLoginExpiry(env(map[string]string{"HOME": home})).ClaudeExpired(now); !ok {
		t.Fatal("~/.claude not read")
	}
	if _, ok := serviceLoginExpiry(env(map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": alt})).ClaudeExpired(now); ok {
		t.Fatal("~/.claude read although CLAUDE_CONFIG_DIR names another dir")
	}
	write(alt)
	if _, ok := serviceLoginExpiry(env(map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": alt})).ClaudeExpired(now); !ok {
		t.Fatal("CLAUDE_CONFIG_DIR not read")
	}
	if c := serviceLoginExpiry(env(nil)); c.ClaudeExpired != nil {
		t.Fatal("read with no home")
	}
}

// The service reads each Claude agent's credentials (login-updates r9 §3):
// its own directory, or ~/.claude for the default; only Claude Code agents;
// the warning threshold from dispatch.login_expiry_warn, read at each call.
func TestServiceLoginExpiryReadsCredentials(t *testing.T) {
	home, alt := t.TempDir(), t.TempDir()
	write := func(dir string, ms int64) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"claudeAiOauth":{"accessToken":"SENTINEL-A","refreshToken":"SENTINEL-R","refreshTokenExpiresAt":` + strconv.FormatInt(ms, 10) + `}}`
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	def, other := now.Add(48*time.Hour).Truncate(time.Millisecond), now.Add(96*time.Hour).Truncate(time.Millisecond)
	write(filepath.Join(home, ".claude"), def.UnixMilli())
	write(alt, other.UnixMilli())
	vars := map[string]string{"HOME": home}
	c := serviceLoginExpiry(func(k string) string { return vars[k] })
	if got, r := c.Credentials("", now); r != loginexpiry.OK || !got.Equal(def) {
		t.Fatalf("default dir: %v %q", got, r)
	}
	if got, r := c.Credentials(alt, now); r != loginexpiry.OK || !got.Equal(other) {
		t.Fatalf("agent's dir: %v %q", got, r)
	}
	if _, r := c.Credentials("relative/dir", now); r != loginexpiry.InvalidPath {
		t.Fatalf("relative dir: %q", r)
	}
	if !c.Checked("claude-code") || c.Checked("codex") || c.Checked("") {
		t.Fatal("only Claude Code agents are checked")
	}
	if w := c.Warn(); w != 72*time.Hour {
		t.Fatalf("default warn %v", w)
	}
	k, _ := config.Lookup("dispatch.login_expiry_warn")
	vars[k.Env] = "0s"
	if w := c.Warn(); w != 0 {
		t.Fatalf("warn off: %v", w)
	}
	// No home: the default directory is not read (a relative path), an agent's own still is.
	c = serviceLoginExpiry(func(string) string { return "" })
	if _, r := c.Credentials("", now); r != loginexpiry.InvalidPath {
		t.Fatalf("no home: %q", r)
	}
}
