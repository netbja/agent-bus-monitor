package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
