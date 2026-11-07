package service

import (
	"os"
	"path/filepath"
	"testing"
)

// The reload policy (auto-reload): the caller's environment, then the
// project's env file (up --reload, per project), then config.toml's
// sentinel.reload (the machine), then on.
func TestReloadPolicyOrder(t *testing.T) {
	xdg := t.TempDir()
	cfg := filepath.Join(xdg, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		env, file, toml string
		mode, source    string
	}{
		"nothing set":             {"", "", "", "on", "default"},
		"config.toml":             {"", "", "propose", "propose", "config.toml"},
		"env file over the toml":  {"", "off", "propose", "off", "env file"},
		"caller over both":        {"propose", "off", "on", "propose", "env AGENT_BUS_RELOAD"},
		"an unknown caller value": {"yes", "off", "", "on", "env AGENT_BUS_RELOAD"},
		"a refused toml value":    {"", "", "yes", "on", "default"},
	} {
		body := ""
		if c.toml != "" {
			body = "[sentinel]\nreload = \"" + c.toml + "\"\n"
		}
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		getenv := func(k string) string {
			switch k {
			case "XDG_CONFIG_HOME":
				return xdg
			case "AGENT_BUS_RELOAD":
				return c.env
			}
			return ""
		}
		envFile := map[string]string{}
		if c.file != "" {
			envFile["AGENT_BUS_RELOAD"] = c.file
		}
		mode, source := ReloadPolicy(getenv, envFile)
		if mode != c.mode || source != c.source {
			t.Errorf("%s: %s from %s, want %s from %s", name, mode, source, c.mode, c.source)
		}
	}
}
