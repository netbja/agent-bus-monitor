package dispatchsvc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

// The dispatcher's reload policy is read at each call: a config.toml change
// (busmon's C page) applies without a restart, and the project's env file
// sits above it (auto-reload).
func TestServiceReloadPolicyReadsEachTime(t *testing.T) {
	xdg := t.TempDir()
	cfg := filepath.Join(xdg, "agentbus", "config.toml")
	envFile := filepath.Join(t.TempDir(), "p.env")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	cmdtest.Write(t, cfg, "[sentinel]\nreload = \"propose\"\n")
	getenv := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	read := serviceReloadPolicy(getenv, envFile)
	if got := read(); got != "propose" {
		t.Fatalf("first = %q", got)
	}
	cmdtest.Write(t, cfg, "[sentinel]\nreload = \"off\"\n")
	if got := read(); got != "off" {
		t.Fatalf("after the C page = %q", got)
	}
	cmdtest.Write(t, envFile, "AGENT_BUS_RELOAD=\"on\"\n")
	if got := read(); got != "on" {
		t.Fatalf("with the env file = %q", got)
	}
}
