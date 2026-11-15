package main

import (
	"bytes"
	"errors"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"os"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

func TestSnapLife(t *testing.T) {
	starts := map[int]uint64{10: 500}
	ps := func(pid int) (uint64, error) {
		if s, ok := starts[pid]; ok {
			return s, nil
		}
		if pid == 13 {
			return 0, errors.New("permission denied")
		}
		return 0, os.ErrNotExist
	}
	for _, c := range []struct {
		name string
		s    bus.AgentSnapshot
		has  bool
		ps   func(int) (uint64, error)
		want relaunch.Life
	}{
		{"no snapshot", bus.AgentSnapshot{}, false, ps, relaunch.LifeUnknown},
		{"no pid", bus.AgentSnapshot{State: "idle"}, true, ps, relaunch.LifeUnknown},
		{"runs with its start", bus.AgentSnapshot{PID: 10, PIDStart: 500}, true, ps, relaunch.LifeAlive},
		{"pid reused", bus.AgentSnapshot{PID: 10, PIDStart: 499}, true, ps, relaunch.LifeDead},
		{"pid gone", bus.AgentSnapshot{PID: 11, PIDStart: 500}, true, ps, relaunch.LifeDead},
		{"unreadable", bus.AgentSnapshot{PID: 13, PIDStart: 500}, true, ps, relaunch.LifeUnknown},
		{"not on Linux", bus.AgentSnapshot{PID: 10, PIDStart: 500}, true, nil, relaunch.LifeUnknown},
	} {
		if got := relaunch.SnapLife(c.s, c.has, c.ps); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestLaunchRole(t *testing.T) {
	for cmd, want := range map[string]string{
		"env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo": "master",
		"agent-launch coder demo --profile x":                              "coder",
		"agent-launch coder other":                                         "",
		"/r/busmon --project demo":                                         "",
		"agent-launch":                                                     "",
		"agent-launch BAD demo":                                            "",
	} {
		got, ok := relaunch.LaunchRole(cmd, "demo")
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", cmd, got, ok, want)
		}
	}
}

func TestUpDryRunProvisionsNothing(t *testing.T) {
	_, templates, _, boot, deps := upFixture(t)
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"demo\"\n[[tabs]]\nname = \"master\"\ncommand = \"" + agentTab(templates, "master") + "\"\n"
	if err := os.WriteFile(templates+"/demo.toml", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", "--dry-run"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	h, _ := deps.Team("demo")
	if len(*boot) != 0 || len(h.(*recHost).specs) != 0 {
		t.Fatalf("boot %q started %v", *boot, h.(*recHost).started())
	}
	if !strings.Contains(out.String(), "dry run: master: would start fresh") {
		t.Fatalf("out = %s", out.String())
	}
}

func TestUpLockExcludesASecondUp(t *testing.T) {
	lock := upLock(t.TempDir())
	unlock, err := lock("demo", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock("demo", false); !errors.Is(err, errUpBusy) {
		t.Fatalf("a second up during an up: %v", err)
	}
	if u, err := lock("other", false); err != nil {
		t.Fatalf("another project is locked: %v", err)
	} else {
		u()
	}
	unlock()
	u2, err := lock("demo", false)
	if err != nil {
		t.Fatalf("an up after an up: %v", err)
	}
	u2()
}

// No lock, no pane: an unwritable cache refuses up.
func TestUpLockFailsClosed(t *testing.T) {
	blocker := t.TempDir() + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := upLock(blocker + "/cache") // a file where the directory should be
	for _, wait := range []bool{true, false} {
		if _, err := lock("demo", wait); err == nil || errors.Is(err, errUpBusy) {
			t.Errorf("wait=%v: %v", wait, err)
		}
	}
	work, _, h, _, deps := upFixture(t)
	deps.Lock = lock
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || len(h.specs) != 0 {
		t.Fatalf("up without its lock: code %d started %v", code, h.started())
	}
}

// The workspace hook starts a stopped service as installed (no Install, so a
// unit is never rewritten), leaves a running one, and touches no pane.
func TestUpHookStartsStoppedServicesOnly(t *testing.T) {
	_, _, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	u.m.specs["demo/dispatch"] = service.Spec{Project: "demo", Kind: "dispatch"}
	u.m.specs["demo/missions"] = service.Spec{Project: "demo", Kind: "missions"}
	u.m.active["demo/missions"] = true
	var out, errw bytes.Buffer
	if code := upHook(deps, "demo", "w4", &out, &errw); code != 0 {
		t.Fatalf("hook = %d: %s", code, errw.String())
	}
	if got := strings.Join(u.m.calls, ","); got != "start demo dispatch" {
		t.Fatalf("manager calls = %q", got)
	}
	if len(*boot) != 0 || !strings.Contains(out.String(), "dispatch started") {
		t.Fatalf("boot %q out %s", *boot, out.String())
	}
}

// ProvedSession is the session a relaunch may resume: the one proved with the
// agent's process, for a client whose session source is verified. A Codex
// session proved by its hook is not resumed until Codex's proof is verified.
func TestProvedSession(t *testing.T) {
	id := "93ff25bd-34d8-4077-bf6c-65e6cb135bbf"
	for _, c := range []struct {
		s    bus.AgentSnapshot
		want string
	}{
		{bus.AgentSnapshot{Client: "claude-code", IdentSession: id}, id},
		{bus.AgentSnapshot{Client: "grok", IdentSession: id}, ""},
		{bus.AgentSnapshot{Client: "codex", IdentSession: id}, ""},
		{bus.AgentSnapshot{Client: "agy", IdentSession: id}, ""},
		{bus.AgentSnapshot{Client: "claude-code", Session: id}, ""}, // unproved
		{bus.AgentSnapshot{Client: "claude-code", IdentSession: "../x"}, ""},
		{bus.AgentSnapshot{IdentSession: id}, ""}, // no client known
	} {
		if got := relaunch.ProvedSession(c.s); got != c.want {
			t.Errorf("%+v: %q, want %q", c.s, got, c.want)
		}
	}
}

// A pop that is gone is named with the command that brings it back, resumed
// only when its session can be: a Codex pop's proved session would be refused
// by the launcher.
func TestPopRecoveryLine(t *testing.T) {
	id := "93ff25bd-34d8-4077-bf6c-65e6cb135bbf"
	if got := popRecovery("ui", "demo", bus.AgentSnapshot{Client: "claude-code", IdentSession: id}); got != "pop ui not relaunched: agent-spawn ui demo --resume "+id {
		t.Fatalf("claude: %q", got)
	}
	if got := popRecovery("ui", "demo", bus.AgentSnapshot{Client: "codex", IdentSession: id}); got != "pop ui not relaunched: agent-spawn ui demo" {
		t.Fatalf("codex: %q", got)
	}
}

func TestIsBusmonTab(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/r/busmon --project demo --as charlie": true,
		"busmon --project=demo":                 true,
		"'/opt/my tools/busmon' --project demo": true,
		"/r/busmon --project shop":              false,
		"/r/busmon":                             false,
		"/r/busmonx --project demo":             false,
		"":                                      false,
	} {
		if got := relaunch.IsBusmonTab(upTab{Name: "busmon", Command: cmd}, "demo"); got != want {
			t.Errorf("%q = %v, want %v", cmd, got, want)
		}
	}
	if relaunch.IsBusmonTab(upTab{Name: "monitor", Command: "/r/busmon --project demo"}, "demo") {
		t.Error("a tab not named busmon")
	}
}
