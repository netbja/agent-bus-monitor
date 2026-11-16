package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// P2-11 (the person's plan): a directive sent from the CLI with no identity
// arrived 'from anonymous', and master's answer went to anonymous (an
// 'anonymous: no snapshot' escalation followed). The sender is the agent
// (AGENT_BUS_AGENT), else the person's recorded identity (identity.as:
// AGENT_BUS_HUMAN, else config.toml); with neither, a write is refused.
func senderEnv(t *testing.T, m map[string]string) func(string) string {
	t.Helper()
	cfg := t.TempDir()
	if body, ok := m["config.toml"]; ok {
		if err := os.MkdirAll(filepath.Join(cfg, "agentbus"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME":
			return cfg
		case "HOME":
			return cfg
		}
		return m[k]
	}
}

func TestSenderIdentity(t *testing.T) {
	human := config.EnvName("identity.as")
	for _, c := range []struct {
		env    map[string]string
		name   string
		person bool
	}{
		{map[string]string{"AGENT_BUS_AGENT": "coder", human: "charlie"}, "coder", false}, // the agent first, always
		{map[string]string{human: "charlie"}, "charlie", true},
		{map[string]string{"config.toml": "[identity]\nas = \"dana\"\n"}, "dana", true}, // recorded once (agentbus identity set)
		{map[string]string{human: "charlie", "config.toml": "[identity]\nas = \"dana\"\n"}, "charlie", true},
		{map[string]string{}, "anonymous", false},
		{map[string]string{human: "Not Valid"}, "anonymous", false},
	} {
		name, person := senderIdentity(senderEnv(t, c.env))
		if name != c.name || person != c.person {
			t.Errorf("%v: %q %v, want %q %v", c.env, name, person, c.name, c.person)
		}
	}
}

func TestIdentityGuardRefusesAnonymousOutsideATerminal(t *testing.T) {
	err := identityGuard(senderEnv(t, map[string]string{}), "cmd", []string{"master", "x"})
	if err == nil {
		t.Fatal("a directive with no identity was allowed")
	}
	for _, want := range []string{"anonymous", config.EnvName("identity.as"), "agentbus identity set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err, want)
		}
	}
	if err := identityGuard(senderEnv(t, map[string]string{}), "reports", nil); err != nil {
		t.Fatalf("a read refused: %v", err)
	}
	if err := identityGuard(senderEnv(t, map[string]string{config.EnvName("identity.as"): "charlie"}), "cmd", []string{"master", "x"}); err != nil {
		t.Fatalf("the person's identity refused: %v", err)
	}
	// Inside an agent's terminal the person's identity never stands in for
	// the agent's: an inherited AGENT_BUS_HUMAN is still refused there.
	pane := senderEnv(t, map[string]string{"AGENTBUS_TERM_ID": "t3", config.EnvName("identity.as"): "charlie"})
	if err := identityGuard(pane, "cmd", []string{"master", "x"}); err == nil || !strings.Contains(err.Error(), "AGENT_BUS_AGENT") {
		t.Fatalf("a terminal without its agent's name: %v", err)
	}
}

// Nothing is addressed to anonymous: it is nobody (report --to, reply, cmd).
func TestNoTargetAnonymous(t *testing.T) {
	err := refuseAnonymousTarget("anonymous")
	if err == nil || !strings.Contains(err.Error(), config.EnvName("identity.as")) || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("anonymous as a target: %v", err)
	}
	if err := refuseAnonymousTarget("master"); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(context.Background(), nil, "master", []string{"anonymous", "x"}, io.Discard); err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("cmd to anonymous: %v", err)
	}
	if _, err := sendReport(context.Background(), nil, []string{"master", "--to", "anonymous", "done"}, io.Discard); err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("report --to anonymous: %v", err)
	}
}
