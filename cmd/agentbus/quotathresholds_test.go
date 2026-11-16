package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// Quota settings step 2 (§2.2): the dispatch service reads its project's
// thresholds from the files at each refresh: config.toml's [project.<p>]
// table, then the project's env file (the shell at up), then the machine
// keys, then the defaults. Its own process environment, a start-time copy,
// is never read for them.
func TestQuotaThresholdsFromTheFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "demo.env")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	getenv := func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME", "HOME":
			return dir
		case config.EnvName("dispatch.quota_warn"): // the process's start-time copy: never read
			return "50"
		}
		return ""
	}
	read := quotaThresholds(getenv, "demo", envFile)
	// Nothing set: the defaults, the drain following the warning.
	th, err := read()
	if err != nil || th.Warn != 85 || th.Drain != 85 || th.Hold != 95 || th.MaxAge == 0 {
		t.Fatalf("defaults: %+v %v", th, err)
	}
	write(cfg, "[dispatch]\nquota_hold = 97\n\n[project.demo.dispatch]\nquota_warn = 80\nquota_drain = 90\n")
	write(envFile, config.EnvName("dispatch.quota_drain")+"=\"70\"\n"+config.EnvName("dispatch.quota_hold")+"=\"96\"\n")
	th, err = read()
	if err != nil || th.Warn != 80 || th.Drain != 90 || th.Hold != 96 {
		t.Fatalf("project table, then env file, then machine: %+v %v", th, err)
	}
	// The project's drain removed (busmon's D): the env file's shows at the
	// next read, though the process still holds its start-time values.
	write(cfg, "[dispatch]\nquota_hold = 97\n\n[project.demo.dispatch]\nquota_warn = 80\n")
	if th, err = read(); err != nil || th.Drain != 70 {
		t.Fatalf("after the removal: %+v %v", th, err)
	}
	// A drain the files never set follows the warning, whatever its source.
	write(envFile, "")
	if th, err = read(); err != nil || th.Drain != 80 {
		t.Fatalf("an unset drain: %+v %v", th, err)
	}
	// A config.toml that does not parse is an error: the dispatcher keeps the last.
	write(cfg, "[dispatch\nquota_warn = 80\n")
	if _, err = read(); err == nil {
		t.Fatal("an unparsable config.toml read as the defaults")
	}
	// No env file at all (a project started before one existed) is no error.
	write(cfg, "")
	if _, err = quotaThresholds(getenv, "demo", filepath.Join(dir, "none.env"))(); err != nil {
		t.Fatalf("no env file: %v", err)
	}
}
