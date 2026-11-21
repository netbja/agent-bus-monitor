package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/setup"
)

// noRunner fails the test on any command: a refusal runs none.
type noRunner struct{ t *testing.T }

func (r noRunner) Run(_ context.Context, c setup.Cmd) ([]byte, error) {
	r.t.Errorf("ran %s %q", c.Name, c.Args)
	return nil, errors.New("no")
}
func (r noRunner) LookPath(string) (string, error) { return "", errors.New("no") }

// ./setup.sh acl on|off is the person's act: an agent's environment is
// refused before any command runs or any password file is read.
func TestSetupACLRefusesAnAgent(t *testing.T) {
	repo, _ := filepath.Abs("../..")
	home := t.TempDir()
	env := map[string]string{"HOME": home, "AGENT_BUS_AGENT": "coder"}
	for _, state := range []string{"on", "off"} {
		var out bytes.Buffer
		code := runSetup([]string{"--repo", repo, "acl", state, "--yes"}, func(k string) string { return env[k] }, noRunner{t}, &out, &out, false)
		if code != 1 || !strings.Contains(out.String(), "the person's act, not coder's") || !strings.Contains(out.String(), "./setup.sh acl "+state) {
			t.Errorf("acl %s: exit %d: %s", state, code, out.String())
		}
	}
}
