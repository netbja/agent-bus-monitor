package main

import (
	"context"
	"strings"
	"testing"
)

// S starts a stopped project's services (project-switch-ux item 1, master:
// agentbus service start, dispatch then missions; the agents stay down):
// it asks with the exact command, y runs it; the person's act, like X.
func startWorld(t *testing.T, env map[string]string, dispatch string) *restartWorld {
	t.Helper()
	w := &restartWorld{done: make(chan error, 1)}
	m := fixtureShell(t, 140, 40)
	m.cfg.project, m.cfg.from = "demo", "trading"
	m.cfg.getenv = func(k string) string { return env[k] }
	m.cfg.ancestor = func(context.Context) (string, error) { return "", nil }
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/services.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return w.done, nil
	}
	m.snap.dispatch = dispatch
	m.refresh()
	w.m = m
	return w
}

func TestServiceStartAsksThenRuns(t *testing.T) {
	w := startWorld(t, nil, "down")
	w.press(t, 'S')
	if want := "Start the services of demo? dispatch, missions; the agents stay down agentbus --project demo service start"; dialogText(w.m) != want {
		t.Fatalf("question %q", dialogText(w.m))
	}
	if len(w.runs) != 0 {
		t.Fatal("ran before y")
	}
	w.press(t, 'y')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "--project demo service start" {
		t.Fatalf("runs %v", w.runs)
	}
	w2 := startWorld(t, nil, "down")
	w2.press(t, 'S', 'n')
	if len(w2.runs) != 0 {
		t.Fatalf("n ran %v", w2.runs)
	}
}

func TestServiceStartRefusals(t *testing.T) {
	for _, c := range []struct {
		name, dispatch string
		env            map[string]string
		busy           bool
		why            string
	}{
		{"an agent's environment", "down", map[string]string{"AGENT_BUS_AGENT": "coder"}, false, "the person's act, not coder's"},
		{"running already", "ready", nil, false, "dispatch is ready"},
		{"unknown", "", nil, false, "dispatch's state is not known yet"},
		{"a run in progress", "down", nil, true, "backup already running"},
	} {
		w := startWorld(t, c.env, c.dispatch)
		if c.busy {
			w.m.ui.sleepRun = &sleepRun{verb: "backup", log: "/x.log"}
		}
		w.press(t, 'S')
		if w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, c.why) {
			t.Errorf("%s: asked %v, flash %q, want %q", c.name, w.m.ui.sleepAsk != nil, w.m.ui.flash, c.why)
		}
		w.press(t, 'y')
		if len(w.runs) != 0 {
			t.Errorf("%s: ran %v", c.name, w.runs)
		}
	}
}

// The banner names the start key.
func TestStoppedBannerStartKey(t *testing.T) {
	w := startWorld(t, nil, "down")
	rows := strings.Split(w.m.grid().plain(), "\n")
	if !strings.Contains(rows[w.m.layout.Header.H], "S start services") {
		t.Fatalf("banner %q", rows[w.m.layout.Header.H])
	}
}
