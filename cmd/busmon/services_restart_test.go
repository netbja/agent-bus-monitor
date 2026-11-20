package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// services-restart-buttons §3: X on a service card asks with the exact
// command, y runs it through the runner busmon runs agentbus with, the
// card is read again after; each refusal sends nothing; an agent's busmon
// asks nothing.

type restartWorld struct {
	m    Model
	runs [][]string
	logs []string
	done chan error
}

func newRestartWorld(t *testing.T, env map[string]string, ancestor string) *restartWorld {
	t.Helper()
	w := &restartWorld{done: make(chan error, 1)}
	host, _ := os.Hostname()
	m := fixtureShell(t, 140, 40)
	m.cfg.project = "demo"
	m.cfg.getenv = func(k string) string { return env[k] }
	m.cfg.ancestor = func(context.Context) (string, error) { return ancestor, nil }
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/services.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return w.done, nil
	}
	m.snap.svcInst = &bus.ServicesInstall{Manager: "systemd", Host: host, Kinds: []string{"dispatch"}}
	m.snap.svcRecs = map[string]bus.ServiceRecord{"dispatch": {State: "ready", Host: host}, "missions": {State: "idle", Host: "far"}}
	next, cmd := m.Update(keyMsg(tea.Key{Code: tea.KeyF3}))
	w.m = run(t, next.(Model), cmd)
	if p := w.m.rd.top(); p == nil || p.kind != ovServices {
		t.Fatal("F3 opened no Services page")
	}
	return w
}

func (w *restartWorld) press(t *testing.T, ks ...any) {
	t.Helper()
	for _, k := range ks {
		next, cmd := w.m.Update(keyMsg(k))
		w.m = run(t, next.(Model), cmd)
	}
}

func TestServiceRestartAsksThenRuns(t *testing.T) {
	w := newRestartWorld(t, nil, "")
	w.press(t, 'X')
	want := "Restart dispatch of demo? agentbus --project demo service restart dispatch. y / n"
	if w.m.ui.flash != want || w.m.ui.sleepAsk == nil {
		t.Fatalf("question %q", w.m.ui.flash)
	}
	if len(w.runs) != 0 {
		t.Fatal("ran before y")
	}
	w.press(t, 'y')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "--project demo service restart dispatch" || w.logs[0] != "/state/agentbus/demo/services.log" {
		t.Fatalf("runs %v logs %v", w.runs, w.logs)
	}
	// Another key cancels: nothing runs.
	w2 := newRestartWorld(t, nil, "")
	w2.press(t, 'X', 'n')
	if len(w2.runs) != 0 || w2.m.ui.sleepAsk != nil {
		t.Fatalf("n ran %v", w2.runs)
	}
}

// After the run the Services page is read again at once, without a second
// tick chain.
func TestServiceRestartRereadsTheCard(t *testing.T) {
	w := newRestartWorld(t, nil, "")
	w.press(t, 'X', 'y')
	r := w.m.ui.sleepRun
	if r == nil {
		t.Fatal("no run in progress")
	}
	cmd := w.m.gotSleepDone(sleepDoneMsg{id: r.id, last: "dispatch: restarted"})
	if cmd == nil || !w.m.rd.top().svcBusy {
		t.Fatal("the Services page was not read again")
	}
	q := w.m.notice.queue
	if len(q) == 0 || !strings.Contains(q[len(q)-1].text, "restart dispatch done") {
		t.Fatalf("notices %+v", q)
	}
}

func TestServiceRestartRefusals(t *testing.T) {
	for _, c := range []struct {
		name     string
		env      map[string]string
		ancestor string
		setup    func(w *restartWorld)
		keys     []any
		why      string
	}{
		{"an agent's environment", map[string]string{"AGENT_BUS_AGENT": "coder"}, "", nil, []any{'X'}, "the person's act, not coder's"},
		{"an agent ancestor", nil, "master", nil, []any{'X'}, "runs under the agent master"},
		{"another host", nil, "", nil, []any{kDown, 'X'}, "restart it on far"},
		{"not installed here", nil, "", func(w *restartWorld) { w.m.rd.top().svc.inst.Kinds = nil }, []any{'X'}, "no dispatch service is installed here"},
		{"the Redis card", nil, "", nil, []any{kDown, kDown, 'X'}, "setup restarts it"},
		{"a run in progress", nil, "", func(w *restartWorld) { w.m.ui.sleepRun = &sleepRun{verb: "backup", log: "/x.log"} }, []any{'X'}, "backup already running"},
	} {
		w := newRestartWorld(t, c.env, c.ancestor)
		if c.setup != nil {
			c.setup(w)
		}
		w.press(t, c.keys...)
		if w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, c.why) {
			t.Errorf("%s: asked %v, flash %q, want %q", c.name, w.m.ui.sleepAsk != nil, w.m.ui.flash, c.why)
		}
		w.press(t, 'y')
		if len(w.runs) != 0 {
			t.Errorf("%s: ran %v", c.name, w.runs)
		}
	}
}

// The footer offers X restart on a service card, not on Redis's.
func TestServiceRestartFooter(t *testing.T) {
	w := newRestartWorld(t, nil, "")
	if !w.m.pageOffers(ActServiceRestart) {
		t.Fatal("X not offered on the dispatch card")
	}
	w.press(t, kDown, kDown)
	if w.m.pageOffers(ActServiceRestart) {
		t.Fatal("X offered on the Redis card")
	}
}
