package main

import (
	"github.com/netbja/agent-bus-monitor/relaunch"
	"io"
	"strings"
	"testing"
)

// askOpen runs the assistant on an existing project shop, every agent of
// its template live but those in dead.
func askOpen(t *testing.T, typed string, dead ...string) (*startWorld, int, string) {
	t.Helper()
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	w.writeTemplate("shop", nil)
	team, err := rawTeam(w.projects + "/shop.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range team {
		w.live[r.Role] = relaunch.LifeAlive
	}
	for _, r := range dead {
		w.live[r] = relaunch.LifeDead
	}
	tty := &fakeTTY{Reader: strings.NewReader(typed)}
	d := w.deps()
	d.TTY = func() io.ReadWriter { return tty }
	d.Look = func(exe string) (string, error) { return "/bin/" + exe, nil }
	code := runStart([]string{w.folder}, d)
	return w, code, tty.screen.String()
}

// start-busmon-pane (the person, 2026-11-06): an existing project, nothing
// to settle and no flag, gets a short screen: one status line saying what
// Enter does, c for the full screen.
func TestStartShowsTheShortScreenForAnOpenProject(t *testing.T) {
	w, code, screen := askOpen(t, "\n")
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
		"Enter: open busmon on it   c: change project, name or team   q: quit",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
}

func TestStartShortScreenSaysWhatEnterDoes(t *testing.T) {
	_, _, screen := askOpen(t, "q\n", "coder", "foureyes")
	for _, want := range []string{"shop is open: 1 of 3 agents live (foureyes, coder not).", "Enter: start foureyes and coder on the holder, then open busmon   c:"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
}

// c opens today's full screen; a digit typed on the short one still goes
// straight to its question, so a habit does not break.
func TestStartShortScreenCAndDigits(t *testing.T) {
	_, code, screen := askOpen(t, "c\nq\n", "coder")
	if code != 0 || !strings.Contains(screen, "1  Project") || !strings.Contains(screen, "4  Team") {
		t.Fatalf("c = %d:\n%s", code, screen)
	}
	if !strings.Contains(screen, "Enter: start coder on the holder, then open busmon   1-4: change   q: quit") {
		t.Fatalf("the full screen's status line:\n%s", screen)
	}
	w, code, screen := askOpen(t, "3\ndora\n\n")
	if code != 0 || len(w.ups) != 1 || !strings.Contains(strings.Join(w.ups[0], " "), "--as dora") || !strings.Contains(screen, "3  You") {
		t.Fatalf("3 = %d, ups %q:\n%s", code, w.ups, screen)
	}
	w, _, _ = askOpen(t, "q\n")
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
