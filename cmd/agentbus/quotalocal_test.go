package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/quota"
)

// agentbus quota lists the local-llm peer's provider only where a peer can
// run: config.toml declares an [llm.<name>] endpoint (foureyes, part 2). With
// no client found and no endpoint, nothing is configured, and that is said.
func TestQuotaListsLocalOnlyWithAnEndpoint(t *testing.T) {
	none := func(string) (string, error) { return "", os.ErrNotExist }
	dir := t.TempDir()
	empty := config.Load(filepath.Join(dir, "absent.toml"), "config.toml")
	if got := quotaListed(quota.Builtins(none), empty); got["local"].Kind != "" {
		t.Fatalf("no endpoint: listed %v, want no local", got)
	}
	// local alone (no client found on a clean host): nothing is listed, so
	// "no quota reader configured" is said again.
	if got := quotaListed(map[string]quota.ProviderConfig{"local": {Kind: "unlimited"}}, empty); len(got) != 0 {
		t.Fatalf("local alone: listed %v, want nothing", got)
	}
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[llm.local]\nurl = \"http://127.0.0.1:11434/v1\"\nmodel = \"m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := quotaListed(quota.Builtins(none), config.Load(p, "config.toml")); got["local"].Kind != "unlimited" {
		t.Fatalf("with an endpoint: %v, want local listed", got)
	}
	found := func(string) (string, error) { return "/bin/x", nil }
	if got := quotaListed(quota.Builtins(found), empty); got["openai"].Kind == "" || got["local"].Kind != "" {
		t.Fatalf("clients found, no endpoint: %v", got)
	}
}
