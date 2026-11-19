package cmdtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// TeamManifest writes a team's roles.toml (claude for master and coder,
// claude-alt coder's fallback, codex for reviewer-codex) and loads it; it
// returns the manifest and the file's path.
func TeamManifest(t *testing.T) (*rolecfg.Manifest, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"claude\"\n" +
		"[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.claude-alt]\nclient = \"claude-code\"\nprovider = \"bedrock\"\nmodel = \"sonnet\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.claude-opus]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.codex-review]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"gpt\"\n" +
		"[roles.master]\ntier = \"boot\"\nskills = []\n" +
		"[roles.coder]\ntier = \"boot\"\nskills = []\nfallback_profiles = [\"claude-alt\"]\n" +
		"[roles.reviewer-codex]\ntier = \"boot\"\nskills = []\nprofile = \"codex-review\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}
