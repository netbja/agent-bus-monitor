package main

import (
	"io"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// host-relay-timeout (the person's list, item 20, 2026-11-16): pop and
// reload from master's terminal failed with 'the dispatcher did not answer
// within 5 s' while the dispatch service ran. The service built its
// dispatcher without the relay (cfg.HostOps), so no directive was ever read:
// only the foreground 'agentbus dispatch' set it (herdr exit 10b). The
// service's dispatcher config now carries the relay's runner, the same one.
func TestServiceDispatcherRunsTheRelay(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	// The config is built, never run: the client is never dialled.
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	cfg, _, err := serviceDispatchConfig(b, "demo", "", paths, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostOps.Run == nil {
		t.Fatal("the dispatch service runs no relay: every host command from an agent's terminal times out")
	}
	if fg := hostOpsConfig(b, "demo", ""); fg.Run == nil {
		t.Fatal("the foreground dispatcher's relay is off")
	}
}
