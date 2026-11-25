package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// busmon-quit-confirm (the person: q quit busmon without warning): q asks
// first, in a popup: y quits; n, Enter or Esc stays; Ctrl-C still quits at
// once. display.confirm_quit (on) off quits at once.

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func pressQuit(t *testing.T, m Model, k tea.Key) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg(k))
	return next.(Model), cmd
}

var kq = tea.Key{Code: 'q', Text: "q"}

func TestQuitAsksFirst(t *testing.T) {
	for name, answer := range map[string]tea.Key{"n": {Code: 'n', Text: "n"}, "enter": {Code: tea.KeyEnter}, "esc": {Code: tea.KeyEscape}} {
		m := shellWith(t, 140, 40, shellConfig{}, false)
		m.cfg.settings.ConfirmQuit = true
		m, cmd := pressQuit(t, m, kq)
		if isQuit(cmd) || m.ui.dialog == nil || !strings.Contains(m.grid().plain(), "Quit busmon?") {
			t.Fatalf("%s: q quit at once, or no popup:\n%s", name, m.grid().plain())
		}
		if !m.busyForModal() {
			t.Fatalf("%s: the popup does not hold the modals", name)
		}
		m, _ = pressQuit(t, m, tea.Key{Code: 'j', Text: "j"}) // another key: the popup stays, nothing moves
		if m.ui.dialog == nil {
			t.Fatalf("%s: another key closed the popup", name)
		}
		m, cmd = pressQuit(t, m, answer)
		if isQuit(cmd) || m.ui.dialog != nil || strings.Contains(m.grid().plain(), "Quit busmon?") {
			t.Fatalf("%s: did not stay", name)
		}
	}
	m := shellWith(t, 140, 40, shellConfig{}, false)
	m.cfg.settings.ConfirmQuit = true
	m, _ = pressQuit(t, m, kq)
	if _, cmd := pressQuit(t, m, tea.Key{Code: 'y', Text: "y"}); !isQuit(cmd) {
		t.Fatal("y did not quit")
	}
	// Ctrl-C quits at once, popup or not.
	m = shellWith(t, 140, 40, shellConfig{}, false)
	m.cfg.settings.ConfirmQuit = true
	if _, cmd := pressQuit(t, m, tea.Key{Code: 'c', Mod: tea.ModCtrl}); !isQuit(cmd) {
		t.Fatal("Ctrl-C asked")
	}
	m, _ = pressQuit(t, m, kq)
	if _, cmd := pressQuit(t, m, tea.Key{Code: 'c', Mod: tea.ModCtrl}); !isQuit(cmd) {
		t.Fatal("Ctrl-C on the popup did not quit")
	}
}

// display.confirm_quit off: q quits at once; the setting is in the registry
// (Display group, default on, busmon) and on the Shift+C page.
func TestQuitConfirmSetting(t *testing.T) {
	m := shellWith(t, 140, 40, shellConfig{}, false)
	m.cfg.settings.ConfirmQuit = false
	if _, cmd := pressQuit(t, m, kq); !isQuit(cmd) {
		t.Fatal("off: q asked")
	}
	if got := loadBusmonSettings(settingsDir(t, "", "", nil), settingsFlags{}); !got.ConfirmQuit {
		t.Fatal("not on by default")
	}
	if got := loadBusmonSettings(settingsDir(t, "[display]\nconfirm_quit = false\n", "", nil), settingsFlags{}); got.ConfirmQuit {
		t.Fatal("the file's false not read")
	}
	if settingLabels["display.confirm_quit"] == "" || settingHelps["display.confirm_quit"] == "" {
		t.Fatal("no label or help on the page")
	}
	mm := &Model{cfg: shellConfig{getenv: settingsDir(t, "[display]\nconfirm_quit = false\n", "", nil)}}
	mm.cfg.settings.ConfirmQuit = true
	if w := mm.applySettings("display.confirm_quit"); w != "applied and saved" || mm.cfg.settings.ConfirmQuit {
		t.Fatalf("apply: %q %v", w, mm.cfg.settings.ConfirmQuit)
	}
}
