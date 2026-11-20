package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// views-redesign §3: two settings say when a tracked request is late,
// board.no_accept_after (30 minutes) and board.no_reply_after (2 hours), 0
// off; in Shift+C's Display group, applied at once.
func TestBoardAlertSettings(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "", "", nil), settingsFlags{})
	if got.NoAcceptAfter != 30*time.Minute || got.NoReplyAfter != 2*time.Hour {
		t.Fatalf("defaults: %v %v", got.NoAcceptAfter, got.NoReplyAfter)
	}
	got = loadBusmonSettings(settingsDir(t, "[board]\nno_accept_after = \"0\"\nno_reply_after = \"45m\"\n", "", nil), settingsFlags{})
	if got.NoAcceptAfter != 0 || got.NoReplyAfter != 45*time.Minute {
		t.Fatalf("file: %v %v", got.NoAcceptAfter, got.NoReplyAfter)
	}
	for _, name := range []string{"board.no_accept_after", "board.no_reply_after"} {
		k, ok := config.Lookup(name)
		if !ok || k.Group() != config.GroupDisplay || k.Kind != config.Duration || !strings.Contains(k.ReadBy, "busmon") {
			t.Fatalf("%s: %+v", name, k)
		}
		if settingLabels[name] == "" || settingHelps[name] == "" {
			t.Fatalf("%s: no label or help on the page", name)
		}
	}
	m := &Model{cfg: shellConfig{getenv: settingsDir(t, "[board]\nno_accept_after = \"10m\"\nno_reply_after = \"1h\"\n", "", nil)}}
	if w := m.applySettings("board.no_accept_after"); w != "applied and saved" || m.cfg.settings.NoAcceptAfter != 10*time.Minute {
		t.Fatalf("no_accept_after: %q %v", w, m.cfg.settings.NoAcceptAfter)
	}
	if w := m.applySettings("board.no_reply_after"); w != "applied and saved" || m.cfg.settings.NoReplyAfter != time.Hour {
		t.Fatalf("no_reply_after: %q %v", w, m.cfg.settings.NoReplyAfter)
	}
}
