package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/notify"
)

func TestNotificationsCheckReadsTheHost(t *testing.T) {
	h := newNotifyHome(t, "[notify]\non = true\ntargets = [\"phone\", \"gone\", \"bad\"]\n")
	cfg := filepath.Join(h.home, ".config", "agentbus")
	os.WriteFile(filepath.Join(cfg, "notify.toml"), []byte("[ntfy.phone]\ntopic = \"agentbus-0123456789\"\n[ntfy.bad]\ntopic = \"x\"\n"), 0o600)
	now := time.Now()
	notify.UpdateLast(notify.LastPath(h.state), func(l *notify.Last) error {
		l.Add("phone", notify.Outcome{Project: "demo", At: now.Add(-time.Minute), Err: "phone: 401 unauthorized"}, true)
		l.SetSources("demo", map[string]time.Time{"plan": now.Add(-20 * time.Minute)}, now)
		l.SetSources("other", map[string]time.Time{"asks": now.Add(-20 * time.Minute)}, now)
		return nil
	})
	got, err := notificationsCheck("demo", h.getenv, func() time.Time { return now })(context.Background())
	if err != nil || got.Status != health.Warn {
		t.Fatalf("%+v %v", got, err)
	}
	for _, s := range []string{"gone: not in notify.toml", "bad: refused", "phone: 401 unauthorized", "plan since"} {
		if !strings.Contains(got.Detail, s) {
			t.Errorf("detail %q lacks %q", got.Detail, s)
		}
	}
	if strings.Contains(got.Detail, "asks") || strings.Contains(got.Detail, "agentbus-0123456789") {
		t.Errorf("detail %q: another project's source, or a topic", got.Detail)
	}
}

func TestNotificationsCheckOffIsUnknown(t *testing.T) {
	h := newNotifyHome(t, "")
	got, _ := notificationsCheck("demo", h.getenv, time.Now)(context.Background())
	if got.Status != health.Unknown {
		t.Fatalf("%+v", got)
	}
}
