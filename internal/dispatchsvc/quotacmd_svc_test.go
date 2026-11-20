package dispatchsvc

import (
	"errors"
	"testing"
	"time"
)

// The service's tokens pass reads Claude Code's projects directory, from
// CLAUDE_CONFIG_DIR first; AGENT_BUS_TOKENS=0 turns it off.
func TestServiceTokens(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return "/home/user", nil }
	if c := serviceTokens(env(nil), home); c.Root != "/home/user/.claude/projects" || c.Every != 5*time.Minute || c.Budget.Files != 64 {
		t.Errorf("default %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}), home); c.Root != "/cfg/projects" {
		t.Errorf("CLAUDE_CONFIG_DIR: %+v", c)
	}
	if c := serviceTokens(env(nil), home); c.CodexRoot != "/home/user/.codex/sessions" {
		t.Errorf("codex default %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"CODEX_HOME": "/cx"}), home); c.CodexRoot != "/cx/sessions" {
		t.Errorf("CODEX_HOME: %+v", c)
	}
	nohome := func() (string, error) { return "", errors.New("no home") }
	if c := serviceTokens(env(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}), nohome); c.Root != "/cfg/projects" || c.CodexRoot != "" {
		t.Errorf("no home, CLAUDE_CONFIG_DIR only: Claude counted, Codex not: %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"CODEX_HOME": "/cx"}), nohome); c.Root != "" || c.CodexRoot != "/cx/sessions" || c.Every == 0 {
		t.Errorf("no home, CODEX_HOME only: Codex counted, Claude not (reviewer-codex): %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"AGENT_BUS_TOKENS": "0"}), home); c.Root != "" || c.CodexRoot != "" {
		t.Errorf("off: %+v", c)
	}
}
