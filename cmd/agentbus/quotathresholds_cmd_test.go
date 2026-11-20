package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
)

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
	_, err = dispatchsvc.Config(serviceDispatchDeps(b, "", "", paths, io.Discard), &cfg)
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
