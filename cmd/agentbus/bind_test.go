package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

type fakeBind struct {
	agent string
	who   *bus.AgentIdent
}

func (f *fakeBind) deps(procs []termhost.Proc, starts map[int]uint64) bindDeps {
	return bindDeps{
		Bind: func(_ context.Context, agent string, who bus.AgentIdent) error {
			f.agent, f.who = agent, &who
			return nil
		},
		Procs: func(_ context.Context, pane string) ([]termhost.Proc, string, error) {
			return procs, "term_x", nil
		},
		Start: func(pid int) (uint64, error) { return starts[pid], nil },
		Boot:  func() string { return "boot-here" },
		Env:   func(k string) string { return map[string]string{"AGENTBUS_TERM_ID": "w2:p1"}[k] },
	}
}

var codexProcs = []termhost.Proc{
	{PID: 1930926, PPID: 1928535, Command: "node /x/bin/codex"},
	{PID: 1930933, PPID: 1930926, Command: "/x/vendor/codex"},
}

func TestBindWithoutPidListsCandidates(t *testing.T) {
	f := &fakeBind{}
	var out, errw bytes.Buffer
	code := runBind(context.Background(), f.deps(codexProcs, nil), []string{"codex", "--pane", "w2:p1"}, &out, &errw)
	if code != 2 || f.who != nil {
		t.Fatalf("code=%d wrote=%v, want exit 2 and nothing written", code, f.who)
	}
	for _, want := range []string{"1930926", "1928535", "node /x/bin/codex", "--pid"} {
		if !strings.Contains(out.String()+errw.String(), want) {
			t.Fatalf("output %q lacks %q", out.String()+errw.String(), want)
		}
	}
}

func TestBindRefusesPidOutsidePane(t *testing.T) {
	f := &fakeBind{}
	var out, errw bytes.Buffer
	code := runBind(context.Background(), f.deps(codexProcs, nil), []string{"codex", "--pid", "777"}, &out, &errw)
	if code == 0 || f.who != nil || !strings.Contains(errw.String(), "not a process of terminal term_x") {
		t.Fatalf("code=%d wrote=%v err=%q", code, f.who, errw.String())
	}
}

func TestBindRecordsPidAndStart(t *testing.T) {
	f := &fakeBind{}
	var out, errw bytes.Buffer
	code := runBind(context.Background(), f.deps(codexProcs, map[int]uint64{1930926: 555}), []string{"codex", "--pid", "1930926"}, &out, &errw)
	if code != 0 || f.who == nil {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
	if f.agent != "codex" || f.who.Pane != "term_x" || f.who.PID != 1930926 || f.who.PIDStart != 555 || f.who.Terminal != "term_x" || f.who.BootID != "boot-here" {
		t.Fatalf("bound %s %+v", f.agent, *f.who)
	}
	if !strings.Contains(out.String(), "bound codex to term_x pid 1930926") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestBindNeedsAPaneOutsideHerdr(t *testing.T) {
	f := &fakeBind{}
	d := f.deps(codexProcs, nil)
	d.Env = func(string) string { return "" }
	var out, errw bytes.Buffer
	if code := runBind(context.Background(), d, []string{"codex", "--pid", "1930926"}, &out, &errw); code == 0 || !strings.Contains(errw.String(), "--pane") {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
	if code := runBind(context.Background(), d, []string{}, &out, &errw); code == 0 {
		t.Fatal("bind with no agent succeeded")
	}
}

// bind records the launcher's AGENT_BUS_TIER with the proof (pop-auto-retire §6).
func TestBindRecordsTier(t *testing.T) {
	f := &fakeBind{}
	d := f.deps(codexProcs, map[int]uint64{1930926: 42})
	d.Env = func(k string) string {
		return map[string]string{"AGENTBUS_TERM_ID": "w2:p1", "AGENT_BUS_TIER": "pop"}[k]
	}
	var out, errw bytes.Buffer
	if code := runBind(context.Background(), d, []string{"codex", "--pid", "1930926"}, &out, &errw); code != 0 {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
	if f.who == nil || f.who.Tier != "pop" {
		t.Fatalf("recorded %+v, want tier pop", f.who)
	}
}

// bind records the agent's CLAUDE_CONFIG_DIR with the proof, so a bound
// agent's login expiry is read from its own directory (login-updates §3.2).
func TestBindRecordsClaudeConfigDir(t *testing.T) {
	f := &fakeBind{}
	d := f.deps(codexProcs, map[int]uint64{1930926: 42})
	d.Env = func(k string) string {
		return map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w2:p1", "CLAUDE_CONFIG_DIR": "/srv/claude-b"}[k]
	}
	var out, errw bytes.Buffer
	if code := runBind(context.Background(), d, []string{"codex", "--pid", "1930926"}, &out, &errw); code != 0 {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
	if f.who == nil || f.who.ClaudeConfigDir != "/srv/claude-b" {
		t.Fatalf("recorded %+v, want /srv/claude-b", f.who)
	}
}
