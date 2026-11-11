package main

import (
	"bytes"
	"errors"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmonQuit is an open workspace whose busmon tab holds one pane, w4:p1,
// left a plain shell after the person quit busmon in it.
func busmonQuit() (*relaunchHerdr, upTemplate) {
	h := &relaunchHerdr{
		tabs:  `{"label":"busmon","tab_id":"w4:t1"}`,
		panes: `{"pane_id":"w4:p1","tab_id":"w4:t1"}`,
		fg:    map[string]string{"w4:p1": "-zsh"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "busmon", Command: "/r/busmon --project demo --as charlie"}}}
	return h, tpl
}

// start-busmon-pane (the person, 2026-11-06): busmon comes back in a new pane
// split from its tab, and the old shell is closed once that pane runs it, if
// a re-read right before still shows one plain interactive shell. No proof
// is waited for (busmon may run in any window, not only herdr), nothing is
// typed into the old pane, and no other pane is touched.
func TestRelaunchOpenClosesBusmonsOldShell(t *testing.T) {
	h, tpl := busmonQuit()
	var out bytes.Buffer
	if err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true, CloseBusmon: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 1 || got[0] != "w4:p1" {
		t.Fatalf("closed %q, want only w4:p1\n%s", got, out.String())
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
	split, run, recheck, closed := -1, -1, -1, -1
	for i, c := range h.calls {
		switch {
		case strings.HasPrefix(c, "pane split w4:p1"):
			split = i
		case c == "pane run w4:p1s /r/busmon --project demo --as charlie":
			run = i
		case c == "pane process-info --pane w4:p1" && run >= 0:
			recheck = i
		case c == "pane close w4:p1":
			closed = i
		}
	}
	if !(split >= 0 && split < run && run < recheck && recheck < closed) {
		t.Fatalf("split %d, run %d, recheck %d, close %d:\n%s", split, run, recheck, closed, strings.Join(h.calls, "\n"))
	}
	for _, w := range []string{
		"busmon: relaunched (new pane; the old one w4:p1 closes if it is still a plain shell)",
		"busmon: old pane w4:p1 closed",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
}

// The re-read decides: a pane busy again, gone or unreadable is left, and
// said; a failed close is an error.
func TestRelaunchOpenLeavesBusmonsOldPaneUnlessStillAShell(t *testing.T) {
	for name, c := range map[string]struct {
		later string // the old pane's foreground at the re-read ("" = gone)
		want  string
	}{
		"busy again": {later: "vim notes", want: "busmon: old pane w4:p1 left as it is (no longer a plain shell)"},
		"gone":       {later: "", want: "busmon: old pane w4:p1 left as it is (unreadable:"},
	} {
		t.Run(name, func(t *testing.T) {
			h, tpl := busmonQuit()
			rh := &rereadHerdr{relaunchHerdr: h, pane: "w4:p1", later: c.later}
			var out bytes.Buffer
			if err := relaunch.Open(rh.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true, CloseBusmon: true}, &out); err != nil {
				t.Fatal(err)
			}
			if got := h.closes(); len(got) != 0 {
				t.Fatalf("closed %q", got)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Fatalf("output lacks %q:\n%s", c.want, out.String())
			}
		})
	}
	h, tpl := busmonQuit()
	h.closeErr = errors.New("pane busy")
	var out bytes.Buffer
	err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true, CloseBusmon: true}, &out)
	if err == nil || !strings.Contains(out.String(), "busmon: old pane w4:p1 not closed: ") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

// rereadHerdr answers the old pane's first process-info (the judgement) as
// the fake does, and every later one with later ("" = no such pane).
type rereadHerdr struct {
	*relaunchHerdr
	pane, later string
	reads       int
}

func (r *rereadHerdr) run(args ...string) ([]byte, error) {
	if len(args) == 4 && args[0] == "pane" && args[1] == "process-info" && args[3] == r.pane {
		r.reads++
		if r.reads > 1 {
			if r.later == "" {
				delete(r.fg, r.pane)
			} else {
				r.fg[r.pane] = r.later
			}
		}
	}
	return r.relaunchHerdr.run(args...)
}

// The person's answer to open question 2: only a busmon tab of one pane has
// it closed. In a tab of several shells, which one busmon ran in cannot be
// told, and one may be the person's own: every pane is left, and said.
func TestRelaunchOpenLeavesEveryPaneOfASplitBusmonTab(t *testing.T) {
	h, tpl := busmonQuit()
	h.panes = `{"pane_id":"w4:p1","tab_id":"w4:t1"},{"pane_id":"w4:p7","tab_id":"w4:t1"}`
	h.fg["w4:p7"] = "bash"
	var out bytes.Buffer
	if err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true, CloseBusmon: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 0 {
		t.Fatalf("closed %q", got)
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
	if want := "busmon: relaunched (new pane; the old ones are left as they are: the tab has 2 panes)"; !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
}

// Only the busmon tab, and only one that runs this project's busmon: a
// dispatch tab (tab mode), a tab named busmon running something else, or
// another project's busmon keep today's rule (the old pane is left).
func TestRelaunchOpenClosesOnlyBusmonsTab(t *testing.T) {
	for name, tab := range map[string]upTab{
		"dispatch tab":    {Name: "dispatch", Command: "/r/agentbus --project demo dispatch"},
		"not busmon":      {Name: "busmon", Command: "/r/watch-logs --project demo"},
		"another project": {Name: "busmon", Command: "/r/busmon --project shop"},
		"no project":      {Name: "busmon", Command: "/r/busmon"},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := busmonQuit()
			h.tabs = `{"label":"` + tab.Name + `","tab_id":"w4:t1"}`
			var out bytes.Buffer
			tpl := upTemplate{Tabs: []upTab{tab}}
			if err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true, CloseBusmon: true}, &out); err != nil {
				t.Fatal(err)
			}
			if got := h.closes(); len(got) != 0 {
				t.Fatalf("closed %q\n%s", got, out.String())
			}
			if !strings.Contains(out.String(), "relaunched (new pane; the old one is left as it is)") {
				t.Fatalf("out:\n%s", out.String())
			}
		})
	}
}

// Without the option (reload's own relaunch, the tests of the agent rule)
// busmon's tab keeps today's behaviour.
func TestRelaunchOpenWithoutCloseBusmonLeavesItsPane(t *testing.T) {
	h, tpl := busmonQuit()
	var out bytes.Buffer
	if err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 0 || !strings.Contains(out.String(), "busmon: relaunched (new pane; the old one is left as it is)") {
		t.Fatalf("closed %q\n%s", got, out.String())
	}
}

func TestRelaunchOpenDryRunSaysItWouldCloseBusmonsShell(t *testing.T) {
	h, tpl := busmonQuit()
	var out bytes.Buffer
	if err := relaunch.Open(h.run, nil, false, nil, "demo", "w4", tpl, relaunch.Options{DryRun: true, Create: true, CloseBusmon: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || len(h.closes()) != 0 {
		t.Fatalf("a dry run acted: %q", h.calls)
	}
	if want := "busmon: would relaunch (new pane; would close the old one w4:p1 if it is still a plain shell)"; !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
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

// Through up on an open workspace, with the agents unreadable: busmon's old
// shell is still closed, since it needs herdr only (foureyes on the spec).
func TestUpClosesBusmonsOldShell(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	h, _ := busmonQuit()
	deps.Herdr = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "workspace list" {
			return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
		}
		return h.run(args...)
	}
	deps.Agents = func(string) (map[string]bus.AgentSnapshot, error) { return nil, errors.New("broker down") }
	deps.Sleep = func(time.Duration) {}
	deps.Lock = func(string, bool) (func(), error) { return func() {}, nil }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := h.closes(); len(got) != 1 || got[0] != "w4:p1" || !strings.Contains(out.String(), "busmon: old pane w4:p1 closed") {
		t.Fatalf("closed %q\n%s", got, out.String())
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
}
