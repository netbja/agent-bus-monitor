package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/quota"
)

// agentbus statusline records the session's context fill (context-alert)
// and still prints the status line.
func TestStatuslineRecordsTheContextFill(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	in := `{"session_id":"abc-1","model":{"display_name":"Opus"},"context_window":{"used_percentage":81.5}}`
	var out bytes.Buffer
	if code := runStatusline(nil, strings.NewReader(in), &out); code != 0 || !strings.Contains(out.String(), "ctx 82%") {
		t.Fatalf("exit %d, line %q", code, out.String())
	}
	if r, ok := quota.ReadContext(quota.ContextDir(), "abc-1"); !ok || r.Pct != 81.5 {
		t.Fatalf("recorded %+v %v", r, ok)
	}
}

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
	cfg := serviceContext(func(string) string { return "" }, found, run)
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
}

// AGENT_BUS_AUTO_CLEAR (lifecycle part B): only an explicit 1 turns the
// auto-clear on, the default until the spec's live checks pass.
func TestServiceClear(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	run := func(context.Context, string, ...string) error { return nil }
	for env, want := range map[string]bool{"": false, "0": false, "1": true, "yes": false, "true": false} {
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
