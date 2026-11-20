package dispatchsvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// The service's context alert: AGENT_BUS_CONTEXT_ALERT (default 80, 0 = off,
// an invalid value keeps the default) and a desktop notification through
// notify-send only when it is on PATH; the runner is a fake, never the real one.
func TestServiceContextAlert(t *testing.T) {
	var ran [][]string
	run := func(_ context.Context, bin string, args ...string) error {
		ran = append(ran, append([]string{bin}, args...))
		return nil
	}
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	for _, c := range []struct {
		env  string
		want float64
	}{{"", 80}, {"0", 0}, {"90", 90}, {"abc", 80}, {"-5", 80}, {"150", 80}} {
		cfg := serviceContext(func(k string) string {
			if k == "AGENT_BUS_CONTEXT_ALERT" {
				return c.env
			}
			return ""
		}, found, run)
		if cfg.Alert != c.want || cfg.Read == nil || cfg.Every != time.Minute || cfg.MaxAge != 30*time.Minute {
			t.Fatalf("%q: alert %v every %v max age %v", c.env, cfg.Alert, cfg.Every, cfg.MaxAge)
		}
	}
	// context-escalation §7: urgent 90 and the deadline 20m by default; the
	// registry's env names set them.
	cfg := serviceContext(func(string) string { return "" }, found, run)
	if cfg.Urgent != 90 || cfg.Deadline != 20*time.Minute {
		t.Fatalf("defaults: urgent %v deadline %v", cfg.Urgent, cfg.Deadline)
	}
	set := map[string]string{"AGENT_BUS_CONTEXT_URGENT": "95", "AGENT_BUS_CONTEXT_HANDOFF_DEADLINE": "0"}
	if c := serviceContext(func(k string) string { return set[k] }, found, run); c.Urgent != 95 || c.Deadline != 0 {
		t.Fatalf("set: urgent %v deadline %v", c.Urgent, c.Deadline)
	}
	if err := cfg.Notify(context.Background(), "agentbus p", "coder context 85%"); err != nil || len(ran) != 1 ||
		strings.Join(ran[0], " ") != "/usr/bin/notify-send -a agentbus agentbus p coder context 85%" {
		t.Fatalf("ran %q, %v", ran, err)
	}
	if cfg := serviceContext(func(string) string { return "" }, missing, run); cfg.Notify != nil {
		t.Fatal("a missing notify-send must be skipped")
	}
}

// The service's prompt notification: AGENT_BUS_PROMPT_GRACE seconds (default
// 30, 0 = off, an invalid value keeps the default), through the same fake
// notify-send runner; none when notify-send is missing.
func TestServicePrompt(t *testing.T) {
	var ran [][]string
	run := func(_ context.Context, bin string, args ...string) error {
		ran = append(ran, append([]string{bin}, args...))
		return nil
	}
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	for env, want := range map[string]time.Duration{"": 30 * time.Second, "0": 0, "90": 90 * time.Second, "abc": 30 * time.Second, "-5": 30 * time.Second, "99999": 30 * time.Second} {
		cfg := servicePrompt(func(k string) string {
			if k == "AGENT_BUS_PROMPT_GRACE" {
				return env
			}
			return ""
		}, found, run)
		if cfg.Grace != want {
			t.Fatalf("%q: grace %v, want %v", env, cfg.Grace, want)
		}
	}
	cfg := servicePrompt(func(string) string { return "" }, found, run)
	if err := cfg.Notify(context.Background(), "agentbus p", "p: coder waits on a prompt in its pane (w2:pA)"); err != nil || len(ran) != 1 ||
		strings.Join(ran[0], " ") != "/usr/bin/notify-send -a agentbus agentbus p p: coder waits on a prompt in its pane (w2:pA)" {
		t.Fatalf("ran %q, %v", ran, err)
	}
	if cfg := servicePrompt(func(string) string { return "" }, missing, run); cfg.Notify != nil {
		t.Fatal("a missing notify-send must be skipped")
	}
	// The stuck screen's ring (agent-screen-blocked §3): 3 minutes by
	// default, the setting's seconds, 0 off.
	for env, want := range map[string]time.Duration{"": 3 * time.Minute, "0": 0, "600": 10 * time.Minute} {
		cfg := servicePrompt(func(k string) string {
			if k == config.EnvName("dispatch.stuck_after") {
				return env
			}
			return ""
		}, found, run)
		if cfg.StuckAfter != want {
			t.Fatalf("%q: stuck after %v, want %v", env, cfg.StuckAfter, want)
		}
	}
}

// AGENT_BUS_AUTO_CLEAR (lifecycle part B; autoclear-default): on by default,
// unset included; 0 is the off switch and 1 is on; any other value is not
// taken (the next source applies, here the default), never read as off.
func TestServiceClear(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	run := func(context.Context, string, ...string) error { return nil }
	for env, want := range map[string]bool{"": true, "0": false, "1": true, "yes": true, "true": true, "garbage": true, "false": true} {
		cfg := serviceClear(func(k string) string {
			if k == "AGENT_BUS_AUTO_CLEAR" {
				return env
			}
			return ""
		}, found, run)
		if cfg.On != want || cfg.Proof != 30*time.Second {
			t.Fatalf("%q: on %v proof %v, want on %v", env, cfg.On, cfg.Proof, want)
		}
	}
	if cfg := serviceClear(func(string) string { return "1" }, found, run); cfg.Notify == nil {
		t.Fatal("no desktop notification for an escalated clear")
	}
}

// context-escalation §3.5: the service reads the compaction from the agent's
// own CLAUDE_CONFIG_DIR, else the service's, else ~/.claude.
func TestServiceCompacted(t *testing.T) {
	id := "93ff25bd-34d8-4077-bf6c-65e6cb135bbf"
	write := func(base string, pre int) {
		dir := filepath.Join(base, "projects", "-p")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-15T21:50:00Z","compactMetadata":{"trigger":"auto","preTokens":%d}}`+"\n", pre)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	agent, home := t.TempDir(), t.TempDir()
	write(agent, 111)
	write(filepath.Join(home, ".claude"), 222)
	c := serviceCompacted(func(string) string { return "" }, func() (string, error) { return home, nil })
	if pre, ok := c(agent, id, 0); !ok || pre != 111 {
		t.Fatalf("the agent's dir: %d %v", pre, ok)
	}
	if pre, ok := c("", id, 0); !ok || pre != 222 {
		t.Fatalf("the default: %d %v", pre, ok)
	}
}
