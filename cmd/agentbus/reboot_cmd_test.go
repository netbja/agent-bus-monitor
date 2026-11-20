package main

import (
	"io"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/service"
)

// The dispatch service's own config judges a reboot (reboot-resume §3):
// its judge reads this project's state directory and this host's boot, and
// the machine's settings.
func TestServiceDispatchConfigJudgesTheReboot(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	withBoot(t, "B7")
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
	if cfg.Reboot == nil {
		t.Fatal("the dispatch service judges no reboot")
	}
}
