package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
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

var write = cmdtest.Write
