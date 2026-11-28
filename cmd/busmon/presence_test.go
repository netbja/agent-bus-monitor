package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// cleaning-up-agents §3, §10 (spec docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md),
// step 1b: busmon's Agents view shows an agent without a terminal by its
// presence, never "no terminal": stopped grey in the no-state group, lost red
// in group 1 with its reason first; the Board says "owner lost"; masterDown
// reads the presence.

// gone is an agent whose holder answered without its terminal.
func goneAgent(owes bus.Owes, required bool) *agentState {
	return &agentState{state: "idle", pane: "k/t2", registered: true,
		host:     hostFacts{termsRead: true},
		presence: presenceFacts{owes: owes, owesRead: true, rolesRead: true, required: required}}
}

func TestPresenceLook(t *testing.T) {
	for name, c := range map[string]struct {
		a     *agentState
		word  string
		role  Role
		group int
		flag  string
	}{
		"live":            {&agentState{state: "idle", pane: "k/t2", host: hostFacts{termsRead: true, live: true}}, "idle", RoleMuted, 4, ""},
		"holder not read": {&agentState{state: "working", pane: "k/t2"}, "working", RoleOK, 3, ""},
		"stopped":         {goneAgent(bus.Owes{}, false), bus.PresenceStopped, RoleDim, 6, ""},
		"lost, owns":      {withGate(goneAgent(bus.Owes{Tasks: []string{"fix-x (accepted)"}}, false)), bus.PresenceLost, RoleError, 1, "owns fix-x (accepted)"},
		"lost, required":  {goneAgent(bus.Owes{}, true), bus.PresenceLost, RoleError, 1, "required: agentbus up brings it back"},
		"owes not read":   {&agentState{state: "idle", pane: "k/t2", host: hostFacts{termsRead: true}, presence: presenceFacts{rolesRead: true}}, bus.PresenceLost, RoleError, 1, "unknown: owes"},
		"roles not read":  {&agentState{state: "idle", pane: "k/t2", host: hostFacts{termsRead: true}, presence: presenceFacts{owesRead: true}}, bus.PresenceLost, RoleError, 1, "unknown: roles"},
	} {
		_, word, role, group := stateLook(c.a)
		if word != c.word || role != c.role || group != c.group {
			t.Errorf("%s: %s %v %d, want %s %v %d", name, word, role, group, c.word, c.role, c.group)
		}
		var flags []string
		for _, f := range agentFlags(c.a, false) {
			flags = append(flags, f.Word)
		}
		got := strings.Join(flags, "|")
		if c.flag != "" && (len(flags) == 0 || flags[0] != c.flag) {
			t.Errorf("%s: flags %q, want %q first", name, got, c.flag)
		}
		if c.flag == "" && strings.Contains(got, "unknown") {
			t.Errorf("%s: a reason on a running or stopped agent: %q", name, got)
		}
		if word == bus.StateNoTerminal {
			t.Errorf("%s: 'no terminal' shown", name)
		}
	}
}

func TestPresenceBoardAndMaster(t *testing.T) {
	now := time.Now()
	agents := map[string]*agentState{"docs": goneAgent(bus.Owes{Tasks: []string{"skill (accepted)"}}, false), "coder": {state: "working", pane: "k/t1", host: hostFacts{termsRead: true, live: true}}}
	r := request{Task: "skill", Owner: "docs", State: "accepted", Tracked: true, Accepted: now.Add(-time.Minute)}
	if a, _ := boardAlert(r, now, boardOpts{agents: agents}); a != "owner lost" {
		t.Errorf("a lost owner's request: %q", a)
	}
	r.Owner = "coder"
	if a, _ := boardAlert(r, now, boardOpts{agents: agents}); a != "" {
		t.Errorf("a running owner's request: %q", a)
	}
	if masterDown(agents, "coder") || !masterDown(agents, "docs") || !masterDown(agents, "nobody") {
		t.Error("masterDown does not read the presence")
	}
	agents["stopped"] = goneAgent(bus.Owes{}, false)
	if !masterDown(agents, "stopped") {
		t.Error("a stopped pilot is up")
	}
}

// owesFake is a bus that owes what it is given.
type owesFake struct {
	snaps map[string]bus.AgentSnapshot
	owes  map[string]bus.Owes
	err   error
}

func (f owesFake) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	return f.snaps, f.err
}
func (f owesFake) ReadOwes(context.Context, map[string]bus.AgentSnapshot) map[string]bus.Owes {
	return f.owes
}

// readPresence: each agent's owes, required from roles.toml, self-repair
// from its setting; no roles file leaves required unread (fail closed); a
// failed agents read is an error (the last facts stay).
func TestReadPresence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.toml")
	toml := "schema_version = 2\n[profiles.p]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n" +
		"[roles.coder]\nprofile = \"p\"\ntier = \"boot\"\nrequired = true\n[roles.docs]\nprofile = \"p\"\ntier = \"pop\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	b := owesFake{snaps: map[string]bus.AgentSnapshot{"coder": {}, "docs": {}},
		owes: map[string]bus.Owes{"coder": {}, "docs": {Tasks: []string{"skill (accepted)"}}}}
	env := func(k string) string {
		switch k {
		case "ROLES_TOML":
			return path
		case config.EnvName("dispatch.self_repair"):
			return "on"
		}
		return ""
	}
	got, err := readPresence(context.Background(), b, env)
	if err != nil {
		t.Fatal(err)
	}
	if c := got["coder"]; !c.owesRead || !c.rolesRead || !c.required || !c.selfRepair {
		t.Errorf("coder: %+v", c)
	}
	if d := got["docs"]; d.required || len(d.owes.Tasks) != 1 {
		t.Errorf("docs: %+v", d)
	}
	noRoles, _ := readPresence(context.Background(), b, func(k string) string {
		switch k {
		case "ROLES_TOML":
			return filepath.Join(t.TempDir(), "absent.toml")
		case config.EnvName("dispatch.self_repair"):
			return "off"
		}
		return ""
	})
	if c := noRoles["coder"]; c.rolesRead || c.required || c.selfRepair || !c.owesRead {
		t.Errorf("no roles file, self-repair off: %+v", c)
	}
	b.err = errors.New("timeout")
	if _, err := readPresence(context.Background(), b, env); err == nil {
		t.Error("a failed agents read is no error")
	}
}

// withGate gives a a gate flag, so its presence reason must sort before it.
func withGate(a *agentState) *agentState { a.gated = 1; return a }
