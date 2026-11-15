package main

import (
	"bytes"
	"strings"
	"testing"
)

// holder drain is the person's command: a shell that carries an agent's
// identity is refused before anything is read or done, with or without
// --ignore-background (reviewer-codex).
func TestHolderDrainRefusesAnAgent(t *testing.T) {
	for _, env := range []map[string]string{
		{"AGENT_BUS_AGENT": "coder"},
		{"AGENTBUS_TERM_ID": "t3"},
	} {
		for _, args := range [][]string{{"demo"}, {"demo", "--ignore-background"}, {"--ignore-background", "demo"}} {
			var out, errw bytes.Buffer
			// An unreachable broker: a refusal must come before any connection.
			code := runHolderDrain("", "unix:///nonexistent/redis.sock", args, func(k string) string { return env[k] }, &out, &errw)
			if code != 2 || !strings.Contains(errw.String(), "the person's command") || out.Len() != 0 {
				t.Fatalf("env %v args %v: exit %d out %q err %q", env, args, code, out.String(), errw.String())
			}
		}
	}
	var out, errw bytes.Buffer
	if code := runHolderDrain("demo", "", []string{"other"}, func(string) string { return "" }, &out, &errw); code != 2 || !strings.Contains(errw.String(), "differ") {
		t.Fatalf("project mismatch: exit %d %q", code, errw.String())
	}
}
