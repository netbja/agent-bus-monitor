package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
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

// reviewer-codex on 0cc1cc2f (spec §2.2): a config.toml that disappears after
// a good read keeps the last thresholds (an error, so the dispatcher keeps
// them); one that never existed reads the defaults.
func TestQuotaThresholdsMissingConfigKeepsTheLast(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "XDG_CONFIG_HOME" || k == "HOME" {
			return dir
		}
		return ""
	}
	read := quotaThresholds(getenv, "demo", filepath.Join(dir, "demo.env"))
	if th, err := read(); err != nil || th.Warn != 85 {
		t.Fatalf("no config.toml at start: %+v %v", th, err)
	}
	// Still none at the next read: no error, the env file still read
	// (foureyes on 07f75a85).
	envFile := filepath.Join(dir, "demo.env")
	if err := os.WriteFile(envFile, []byte(config.EnvName("dispatch.quota_warn")+"=\"75\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if th, err := read(); err != nil || th.Warn != 75 {
		t.Fatalf("no config.toml, read again: %+v %v", th, err)
	}
	if err := os.Remove(envFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[project.demo.dispatch]\nquota_warn = 80\nquota_drain = 90\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if th, err := read(); err != nil || th.Warn != 80 || th.Drain != 90 {
		t.Fatalf("a good read: %+v %v", th, err)
	}
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if th, err := read(); err == nil {
		t.Fatalf("config.toml removed read as %+v, not an error", th)
	}
}

// foureyes on 0cc1cc2f: the dispatch service wires the file reader, so its
// project's [project.<p>] table governs the drain.
func TestServiceDispatcherReadsTheProjectThresholds(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	cfgFile := filepath.Join(root, "cfg", "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgFile), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "[dispatch]\nquota = true\n\n[project.demo.dispatch]\nquota_warn = 70\nquota_drain = 80\n"
	if err := os.WriteFile(cfgFile, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	// The config is built, never run: the client is never dialled.
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	var cfg dispatch.Config
	_, err = serviceDispatchConfig(serviceDispatchDeps(b, "", "", paths, io.Discard), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.Read == nil || cfg.Quota.Thresholds == nil {
		t.Fatalf("quota on, thresholds reader wired: read %v, thresholds %v", cfg.Quota.Read != nil, cfg.Quota.Thresholds != nil)
	}
	if th, err := cfg.Quota.Thresholds(); err != nil || th.Warn != 70 || th.Drain != 80 {
		t.Fatalf("the project's table: %+v %v", th, err)
	}
}
