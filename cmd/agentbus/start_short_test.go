package main

import (
	"io"
	"strings"
	"testing"
)

// askOpen runs the assistant on an existing project shop, every agent of
// its template live but those in dead, busmon's tab judged busmon.
func askOpen(t *testing.T, typed string, busmon relaunchVerdict, dead ...string) (*startWorld, int, string) {
	t.Helper()
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	w.writeTemplate("shop", nil)
	team, err := rawTeam(w.projects + "/shop.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range team {
		w.live[r.Role] = lifeAlive
	}
	for _, r := range dead {
		w.live[r] = lifeDead
	}
	tty := &fakeTTY{Reader: strings.NewReader(typed)}
	d := w.deps()
	d.TTY = func() io.ReadWriter { return tty }
	d.Look = func(exe string) (string, error) { return "/bin/" + exe, nil }
	d.Busmon = func(project string) (relaunchVerdict, error) {
		if project != "shop" {
			t.Errorf("busmon judged for %q", project)
		}
		return busmon, nil
	}
	code := runStart([]string{w.folder}, d)
	return w, code, tty.screen.String()
}

// start-busmon-pane (the person, 2026-11-06): an existing project, nothing
// to settle and no flag, gets a short screen: one status line saying what
// Enter does, c for the full screen.
func TestStartShowsTheShortScreenForAnOpenProject(t *testing.T) {
	w, code, screen := askOpen(t, "\n", verdictRunning)
	if code != 0 || len(w.ups) != 1 {
		t.Fatalf("Enter = %d, ups %q\n%s", code, w.ups, screen)
	}
	for _, line := range []string{"1  Project", "2  Folder", "3  You", "4  Team"} {
		if strings.Contains(screen, line) {
			t.Errorf("short screen shows %q:\n%s", line, screen)
		}
	}
	for _, want := range []string{
		"agentbus start — " + w.folder,
		"shop is running: 3 of 3 agents live.",
		"Enter: focus its workspace   c: change project, name or team   q: quit",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
}

func TestStartShortScreenSaysWhatEnterDoes(t *testing.T) {
	for name, c := range map[string]struct {
		busmon relaunchVerdict
		dead   []string
		status string
		enter  string
	}{
		"busmon a shell":     {busmon: verdictRelaunch, status: "shop is open: 3 of 3 agents live, busmon not running (its pane is a shell).", enter: "Enter: reopen busmon   c:"},
		"busmon tab missing": {busmon: verdictCreate, status: "shop is open: 3 of 3 agents live, busmon's tab is missing.", enter: "Enter: reopen busmon   c:"},
		"agents dead":        {busmon: verdictRunning, dead: []string{"coder", "foureyes"}, status: "shop is open: 1 of 3 agents live (foureyes, coder not).", enter: "Enter: relaunch foureyes and coder in new panes   c:"},
		"both":               {busmon: verdictRelaunch, dead: []string{"coder"}, status: "shop is open: 2 of 3 agents live (coder not), busmon not running (its pane is a shell).", enter: "Enter: relaunch coder in new panes, reopen busmon   c:"},
		"busmon unknown":     {busmon: "", status: "shop is running: 3 of 3 agents live.", enter: "Enter: focus its workspace   c:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, screen := askOpen(t, "q\n", c.busmon, c.dead...)
			for _, want := range []string{c.status, c.enter} {
				if !strings.Contains(screen, want) {
					t.Errorf("screen lacks %q:\n%s", want, screen)
				}
			}
		})
	}
}

// c opens today's full screen; a digit typed on the short one still goes
// straight to its question, so a habit does not break.
func TestStartShortScreenCAndDigits(t *testing.T) {
	_, code, screen := askOpen(t, "c\nq\n", verdictRelaunch)
	if code != 0 || !strings.Contains(screen, "1  Project") || !strings.Contains(screen, "4  Team") {
		t.Fatalf("c = %d:\n%s", code, screen)
	}
	if !strings.Contains(screen, "Enter: reopen busmon   1-4: change   q: quit") {
		t.Fatalf("the full screen's status line lacks busmon:\n%s", screen)
	}
	w, code, screen := askOpen(t, "3\ndora\n\n", verdictRunning)
	if code != 0 || len(w.ups) != 1 || !strings.Contains(strings.Join(w.ups[0], " "), "--as dora") || !strings.Contains(screen, "3  You") {
		t.Fatalf("3 = %d, ups %q:\n%s", code, w.ups, screen)
	}
	w, _, _ = askOpen(t, "q\n", verdictRelaunch)
	if len(w.ups) != 0 {
		t.Fatalf("q ran up: %q", w.ups)
	}
}

// A new project, a flag, or something left to settle keeps the full screen.
func TestStartKeepsTheFullScreen(t *testing.T) {
	w := newStartWorld(t)
	if _, screen := w.ask("q\n"); !strings.Contains(screen, "1  Project") {
		t.Fatalf("a new project:\n%s", screen)
	}
	w = newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	w.writeTemplate("shop", nil)
	tty := &fakeTTY{Reader: strings.NewReader("q\n")}
	d := w.deps()
	d.TTY = func() io.ReadWriter { return tty }
	d.Look = func(exe string) (string, error) { return "/bin/" + exe, nil }
	runStart([]string{w.folder, "--as", "dora"}, d)
	if !strings.Contains(tty.screen.String(), "1  Project") {
		t.Fatalf("with --as:\n%s", tty.screen.String())
	}
	w = newStartWorld(t)
	w.writeTemplate("shop", nil) // no name recorded: You is unsettled
	w.env["USER"] = ""
	if _, screen := w.ask("q\n"); !strings.Contains(screen, "1  Project") {
		t.Fatalf("something to settle:\n%s", screen)
	}
}

// busmonVerdict judges the open workspace's busmon tab with up's own rule,
// reading herdr only: unknown ("") when the project is not open or has no
// busmon tab.
func TestBusmonVerdict(t *testing.T) {
	h, tpl := busmonQuit()
	ws := func(h *relaunchHerdr, open bool) func(args ...string) ([]byte, error) {
		return func(args ...string) ([]byte, error) {
			if strings.Join(args, " ") == "workspace list" {
				if !open {
					return []byte(`{"result":{"workspaces":[]}}`), nil
				}
				return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
			}
			return h.run(args...)
		}
	}
	if v, err := busmonVerdict(ws(h, true), tpl, "demo"); err != nil || v != verdictRelaunch {
		t.Fatalf("shell: %q %v", v, err)
	}
	if bad := h.ran(); len(bad) != 0 || len(h.closes()) != 0 {
		t.Fatalf("judging acted: %q", h.calls)
	}
	h.fg["w4:p1"] = "/r/busmon --project demo"
	if v, _ := busmonVerdict(ws(h, true), tpl, "demo"); v != verdictRunning {
		t.Fatalf("running: %q", v)
	}
	if v, _ := busmonVerdict(ws(h, false), tpl, "demo"); v != "" {
		t.Fatalf("not open: %q", v)
	}
	if v, _ := busmonVerdict(ws(h, true), upTemplate{Tabs: []upTab{{Name: "coder", Command: "agent-launch coder demo"}}}, "demo"); v != "" {
		t.Fatalf("no busmon tab: %q", v)
	}
}
