package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

type fakeBind struct {
	agent string
	who   *bus.AgentIdent
}

func (f *fakeBind) deps(procs []dispatch.Proc, starts map[int]uint64) bindDeps {
	return bindDeps{
		Bind: func(_ context.Context, agent string, who bus.AgentIdent) error {
			f.agent, f.who = agent, &who
			return nil
		},
		Procs: func(_ context.Context, pane string) ([]dispatch.Proc, string, error) {
			return procs, "term_x", nil
		},
		Start: func(pid int) (uint64, error) { return starts[pid], nil },
		Env:   func(k string) string { return map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w2:p1"}[k] },
	}
}

var codexProcs = []dispatch.Proc{
	{PID: 1930926, PPID: 1928535, Name: "MainThread", Cmdline: "node /x/bin/codex"},
	{PID: 1930933, PPID: 1930926, Name: "codex", Cmdline: "/x/vendor/codex"},
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
	if code == 0 || f.who != nil || !strings.Contains(errw.String(), "not in the foreground of pane w2:p1") {
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
	if f.agent != "codex" || f.who.Pane != "w2:p1" || f.who.PID != 1930926 || f.who.PIDStart != 555 || f.who.Terminal != "term_x" {
		t.Fatalf("bound %s %+v", f.agent, *f.who)
	}
	if !strings.Contains(out.String(), "bound codex to w2:p1 pid 1930926") {
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
