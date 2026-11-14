package main

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// runQuits runs cmd like run, and says whether it asked to quit.
func runQuits(t *testing.T, m Model, cmd tea.Cmd) (Model, bool) {
	t.Helper()
	if cmd == nil {
		return m, false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return m, true
	case tea.BatchMsg:
		quit := false
		for _, c := range msg {
			var q bool
			m, q = runQuits(t, m, c)
			quit = quit || q
		}
		return m, quit
	default:
		next, more := m.Update(msg) // the command Update returns may be the quit
		return runQuits(t, next.(Model), more)
	}
}

// switchShell is the Projects page's fixture with a broker whose projects
// with keys are the fixture's on-broker ones.
func switchShell(t *testing.T) Model {
	t.Helper()
	src := adminFixture(shellNow)
	m := shellWith(t, 140, 40, shellConfig{admin: &src,
		projectHasKeys: func(_ context.Context, p string) (bool, error) {
			return p == "trading" || p == "agent-bus-monitor", nil
		},
		listProjects: func(context.Context) ([]string, error) { return []string{"agent-bus-monitor", "trading"}, nil }}, false)
	return run(t, m, m.projectNamesCmd()) // Init's read of the broker's projects
}

// openProjectsPage is M, its rows read.
func openProjectsPage(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := pressCmd(t, m, 'M')
	return run(t, m, cmd)
}

// The two gestures (project-switch note §2.1) are one action: o on the
// Projects page and /project <name> both end busmon with the target set,
// for main to save the cache and re-execute.
func TestSwitchProjectGestures(t *testing.T) {
	pinLocal(t)
	m := openProjectsPage(t, switchShell(t))
	m = keys(t, m, 'j') // trading
	if row := m.rd.top().proj.rows[m.rd.top().sel]; row.name != "trading" {
		t.Fatalf("selected %q", row.name)
	}
	m, cmd := pressCmd(t, m, 'o')
	m, quit := runQuits(t, m, cmd)
	if !quit || m.switchTo != "trading" {
		t.Fatalf("o: quit %v switchTo %q flash %q", quit, m.switchTo, m.ui.flash)
	}

	p, _ := palShellWith(t, switchShell(t))
	p = palette(t, p, "/project trading")
	p, cmd = pressCmd(t, p, kEnter)
	p, quit = runQuits(t, p, cmd)
	if !quit || p.switchTo != "trading" {
		t.Fatalf("/project: quit %v switchTo %q flash %q", quit, p.switchTo, p.ui.flash)
	}
}

// /project's argument: a bad name runs nothing and keeps the text (the
// palette's Enter rule, case 2); its completions are the broker's projects.
func TestPaletteProject(t *testing.T) {
	pinLocal(t)
	m, _ := palShellWith(t, switchShell(t))
	m = palette(t, m, "/project Not-A-Name")
	m, cmd := pressCmd(t, m, kEnter)
	m, quit := runQuits(t, m, cmd)
	if quit || m.switchTo != "" || m.ui.edit != "/project Not-A-Name" || !m.ui.flashErr {
		t.Fatalf("a bad name: quit %v switchTo %q edit %q flash %q", quit, m.switchTo, m.ui.edit, m.ui.flash)
	}
	m, _ = palShellWith(t, switchShell(t))
	m = palette(t, m, "/project tr", kTab)
	if m.ui.edit != "/project trading " { // Tab ends the argument with a space, as for /write
		t.Fatalf("Tab: %q", m.ui.edit)
	}
}

// Refusals (§2.2, §2.4): the same project, a project with no keys on this
// broker, an open draft. Nothing switches, the flash says why.
func TestSwitchProjectRefusals(t *testing.T) {
	pinLocal(t)
	try := func(m Model, target string) (Model, bool) {
		cmd := m.switchProject(target)
		return runQuits(t, m, cmd)
	}
	for name, c := range map[string]struct {
		prep   func(*Model)
		target string
		say    string
	}{
		"same project": {nil, "agent-bus-monitor", "already on agent-bus-monitor"},
		"no keys":      {nil, "newone", "not on this broker"},
		"typed line":   {func(m *Model) { m.ui.msgDraft = "half a sentence" }, "trading", "a draft is open"},
		"compose open": {func(m *Model) { m.ui.msgCompose.body = "a long message" }, "trading", "a draft is open"},
		"answer typed": {func(m *Model) { m.ui.askDrafts["q1"] = "yes" }, "trading", "a draft is open"},
	} {
		m := switchShell(t)
		if c.prep != nil {
			c.prep(&m)
		}
		m, quit := try(m, c.target)
		if quit || m.switchTo != "" || !strings.Contains(m.ui.flash, c.say) {
			t.Errorf("%s: quit %v switchTo %q flash %q", name, quit, m.switchTo, m.ui.flash)
		}
	}
}

// palShellWith is a given shell for the palette helpers.
func palShellWith(t *testing.T, m Model) (Model, *fakeWriter) { return m, nil }

// main's end of a switch (§2.2, §3): the old project's cache is on disk when
// execFn is entered, the argv is switchArgv's; a cache that cannot be
// written means no exec; an exec that fails says the command to type.
func TestFinishSwitch(t *testing.T) {
	pinLocal(t)
	dir := t.TempDir()
	m := switchShell(t)
	m.cfg.cachePath = dir + "/agent-bus-monitor.json"
	m.cache_().Read = []string{"1-0"}
	m.sig.cache.Read = []string{"1-0"}
	m.switchTo = "trading"
	opts, set, err := parseFlags([]string{"-p", "agent-bus-monitor", "-H", "vdr"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	saved := execFn
	t.Cleanup(func() { execFn = saved })
	var calls [][]string
	execFn = func(path string, argv, env []string) error {
		c, err := loadBusmonCache(m.cfg.cachePath, "agent-bus-monitor")
		if err != nil || len(c.Read) != 1 || c.Read[0] != "1-0" {
			t.Errorf("the cache was not on disk when exec ran: %+v %v", c, err)
		}
		calls = append(calls, argv)
		return errors.New("exec failed")
	}
	var errw strings.Builder
	if code := finishSwitch(m, opts, set, &errw); code != 1 || len(calls) != 1 {
		t.Fatalf("exit %d, %d execs", code, len(calls))
	}
	if got := calls[0][1:]; !slices.Equal(got, []string{"--host=vdr", "--project=trading"}) {
		t.Fatalf("argv %v", got)
	}
	if !strings.Contains(errw.String(), "busmon --host=vdr --project=trading") {
		t.Fatalf("stderr %q", errw.String())
	}

	// A cache that cannot be written: no exec.
	calls = nil
	blocker := dir + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m.cfg.cachePath = blocker + "/x.json"
	errw.Reset()
	if code := finishSwitch(m, opts, set, &errw); code != 1 || len(calls) != 0 || !strings.Contains(errw.String(), "not saved") {
		t.Fatalf("exit %d, %d execs, %q", code, len(calls), errw.String())
	}
}

// The start fallback (project-switch note §2.5): with nothing naming a
// project, the picker's screen opens one in this busmon. Its Enter checks
// the project on the broker as always, then records the choice instead of
// running agentbus up; an absent project is still refused.
func TestPickerOpensHere(t *testing.T) {
	var chosen string
	m := newOpenHerePick(context.Background(), "/tmp/templates", pickFixture(), nil,
		func(context.Context, string) ([]string, error) { return []string{"agent-bus-monitor:cmd"}, nil }, &chosen)
	m.width, m.height = 120, 30
	m.refresh()
	m, cmd := pickPress(t, m, kEnter)
	m, quit := pickRun(t, m, cmd)
	if !quit || chosen != "agent-bus-monitor" {
		t.Fatalf("quit %v chosen %q status %q", quit, chosen, m.status)
	}
	if strings.Contains(m.busy+m.status, "agentbus up") {
		t.Fatalf("the open-here picker speaks of agentbus up: %q %q", m.busy, m.status)
	}
	chosen = ""
	m = newOpenHerePick(context.Background(), "/tmp/templates", pickFixture(), nil,
		func(context.Context, string) ([]string, error) { return nil, nil }, &chosen)
	m.width, m.height = 120, 30
	m.refresh()
	m, cmd = pickPress(t, m, kEnter)
	if m, quit = pickRun(t, m, cmd); quit || chosen != "" {
		t.Fatalf("an absent project was opened: quit %v chosen %q", quit, chosen)
	}
}

// The picked project is opened by the same exec as a switch; an exec that
// fails says the command to type.
func TestOpenPicked(t *testing.T) {
	opts, set, err := parseFlags([]string{"-H", "vdr"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	saved := execFn
	t.Cleanup(func() { execFn = saved })
	var argv []string
	execFn = func(_ string, a, _ []string) error { argv = a; return errors.New("exec failed") }
	var errw strings.Builder
	if code := openPicked("trading", opts, set, &errw); code != 1 || !slices.Equal(argv[1:], []string{"--host=vdr", "--project=trading"}) {
		t.Fatalf("exit %d argv %v", code, argv)
	}
	if !strings.Contains(errw.String(), "busmon --host=vdr --project=trading") {
		t.Fatalf("stderr %q", errw.String())
	}
}
