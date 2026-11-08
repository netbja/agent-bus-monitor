package main

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// idleShell is the fixture shell with the idle view after `after`; its first
// poll is at shellNow.
func idleShell(t testing.TB, w, h int, after time.Duration) Model {
	t.Helper()
	return shellWith(t, w, h, shellConfig{settings: busmonSettings{IdleView: after}}, false)
}

// pollAt is one poll of the fixture at shellNow+d.
func pollAt(t testing.TB, m Model, d time.Duration) Model {
	t.Helper()
	snap := shellFixture(shellNow.Add(d))
	snap.pause = nil
	for _, a := range snap.agents {
		a.judgedAt = shellNow.Add(d)
	}
	return step(t, m, pollMsg{snap: snap, at: shellNow.Add(d)})
}

// idle view §3: off by default, whatever the time without input.
func TestIdleViewOffByDefault(t *testing.T) {
	m := idleShell(t, 140, 40, 0)
	if m = pollAt(t, m, 3*time.Hour); m.ui.idle {
		t.Fatal("idle view entered while off")
	}
}

// reviewer-codex on the spec: a busmon started and left alone counts its
// delay from its first poll, never from a zero time.
func TestIdleViewDelayFromFirstPoll(t *testing.T) {
	m := idleShell(t, 140, 40, 10*time.Minute)
	if m.ui.idle || !m.ui.lastInput.Equal(shellNow) {
		t.Fatalf("first poll: idle %v lastInput %v", m.ui.idle, m.ui.lastInput)
	}
	if m = pollAt(t, m, 10*time.Minute-time.Second); m.ui.idle {
		t.Fatal("entered before the delay")
	}
	if m = pollAt(t, m, 10*time.Minute); !m.ui.idle {
		t.Fatal("not entered at the delay")
	}
}

// Every input message resets the delay; a tick, a tail batch, a resize, a
// fade frame or a blur does not.
func TestIdleViewInputResets(t *testing.T) {
	inputs := map[string]tea.Msg{
		"key":    tea.KeyPressMsg(tea.Key{Code: tea.KeyF12}),
		"paste":  tea.PasteMsg{Content: "x"},
		"click":  tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseRight}),
		"wheel":  tea.MouseWheelMsg(tea.Mouse{X: 0, Y: 30, Button: tea.MouseWheelDown}),
		"motion": tea.MouseMotionMsg(tea.Mouse{X: 3, Y: 3}),
		"focus":  tea.FocusMsg{},
	}
	for name, msg := range inputs {
		m := idleShell(t, 140, 40, 10*time.Minute)
		m = pollAt(t, m, 5*time.Minute)
		m = step(t, m, msg)
		if !m.ui.lastInput.Equal(shellNow.Add(5 * time.Minute)) {
			t.Errorf("%s: lastInput %v", name, m.ui.lastInput)
		}
		if m = pollAt(t, m, 10*time.Minute); m.ui.idle {
			t.Errorf("%s: entered 5m after the input", name)
		}
		if m = pollAt(t, m, 15*time.Minute); !m.ui.idle {
			t.Errorf("%s: not entered 10m after the input", name)
		}
	}
	others := map[string]tea.Msg{
		"tick":   tickMsg(shellNow.Add(5 * time.Minute)),
		"tail":   tailMsg{},
		"resize": tea.WindowSizeMsg{Width: 140, Height: 40},
		"fade":   fadeMsg(shellNow.Add(5 * time.Minute)),
		"blur":   tea.BlurMsg{},
	}
	for name, msg := range others {
		m := idleShell(t, 140, 40, 10*time.Minute)
		m = pollAt(t, m, 5*time.Minute)
		m = step(t, m, msg)
		if m = pollAt(t, m, 10*time.Minute); !m.ui.idle {
			t.Errorf("%s reset the delay", name)
		}
	}
}

// foureyes on the spec: never over a draft, a question being answered or a
// dialog: every busyForModal member holds it back, and so does TooSmall.
func TestIdleViewWaitsForModals(t *testing.T) {
	for name, set := range map[string]func(*Model){
		"compose":  func(m *Model) { m.ui.compose = &composeState{} },
		"reader":   func(m *Model) { m.rd.pages = append(m.rd.pages, page{kind: ovHelp, title: "help"}) },
		"line":     func(m *Model) { m.ui.line = lineFilter },
		"draft":    func(m *Model) { m.ui.msgDraft = "@coder half a sentence" },
		"chooser":  func(m *Model) { m.ui.askChoose = &askChoice{} },
		"pause":    func(m *Model) { m.ui.pausePopup = &pauseDialog{} },
		"reminder": func(m *Model) { m.ui.reminder = &reminderDialog{} },
		"summary":  func(m *Model) { m.ui.summary = true },
		"tour":     func(m *Model) { m.ui.tour = &tourState{} },
		"walk":     func(m *Model) { m.ui.walk = &walk{} },
	} {
		m := idleShell(t, 140, 40, time.Minute)
		m.now = shellNow.Add(time.Hour) // long past the delay: only the modal holds it back
		set(&m)
		if m.enterIdle(); m.ui.idle {
			t.Errorf("%s: entered over it", name)
		}
	}
	free := idleShell(t, 140, 40, time.Minute)
	free.now = shellNow.Add(time.Hour)
	if free.enterIdle(); !free.ui.idle {
		t.Fatal("not entered with nothing open")
	}
	m := idleShell(t, 30, 5, time.Minute)
	if m = pollAt(t, m, time.Hour); m.ui.idle {
		t.Error("entered at TooSmall")
	}
}

// §3 leaving: the first input wakes the view and does nothing else; q never
// quits.
func TestIdleViewWakeIsDropped(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"q":      tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}),
		"enter":  tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}),
		"digit":  tea.KeyPressMsg(tea.Key{Code: '3', Text: "3"}),
		"click":  tea.MouseClickMsg(tea.Mouse{X: 70, Y: 20, Button: tea.MouseLeft}),
		"wheel":  tea.MouseWheelMsg(tea.Mouse{X: 70, Y: 20, Button: tea.MouseWheelDown}),
		"motion": tea.MouseMotionMsg(tea.Mouse{X: 3, Y: 3}),
		"focus":  tea.FocusMsg{},
		"paste":  tea.PasteMsg{Content: "pasted"},
	} {
		m := pollAt(t, idleShell(t, 140, 40, time.Minute), time.Hour)
		if !m.ui.idle {
			t.Fatalf("%s: not idle", name)
		}
		before := m.ui
		if cmd := m.update(msg); cmd != nil {
			t.Errorf("%s: the wake returned a command", name)
		}
		if m.ui.idle || !m.ui.lastInput.Equal(m.now) {
			t.Errorf("%s: idle %v lastInput %v", name, m.ui.idle, m.ui.lastInput)
		}
		before.idle, before.lastInput, before.idleSince = false, m.ui.lastInput, m.ui.idleSince
		if m.ui.focus != before.focus || m.ui.tab != before.tab || m.ui.view != before.view || m.ui.line != before.line ||
			m.ui.edit != before.edit || m.ui.expanded != before.expanded || m.rd.open() {
			t.Errorf("%s: the wake acted: %+v", name, m.ui)
		}
	}
}

// foureyes on the spec: a reminder due while the view is shown waits; it
// opens once the view is left, on the dashboard.
func TestIdleViewHoldsTheReminder(t *testing.T) {
	m := pollAt(t, idleShell(t, 140, 40, time.Minute), time.Hour)
	if !m.ui.idle {
		t.Fatal("not idle")
	}
	m.cfg.settings.PopupAfter = time.Minute // the fixture's ask is long due
	if m = pollAt(t, m, time.Hour+time.Second); m.ui.reminder != nil || !m.ui.idle {
		t.Fatalf("reminder over the idle view: %v idle %v", m.ui.reminder != nil, m.ui.idle)
	}
	m = step(t, m, tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if m.ui.idle || m.ui.reminder == nil {
		t.Fatalf("after the wake: idle %v reminder %v", m.ui.idle, m.ui.reminder != nil)
	}
}

// §3 the mouse: every motion and focus only while the view is shown.
func TestIdleViewMouseMode(t *testing.T) {
	m := idleShell(t, 140, 40, time.Minute)
	if v := m.View(); v.MouseMode != tea.MouseModeCellMotion || v.ReportFocus {
		t.Fatalf("dashboard: %v focus %v", v.MouseMode, v.ReportFocus)
	}
	m = pollAt(t, m, time.Hour)
	if v := m.View(); v.MouseMode != tea.MouseModeAllMotion || !v.ReportFocus {
		t.Fatalf("idle view: %v focus %v", v.MouseMode, v.ReportFocus)
	}
}

// §6: no fade frame is scheduled while the view is shown; notices wait.
func TestIdleViewNoFade(t *testing.T) {
	m := pollAt(t, idleShell(t, 140, 40, time.Minute), time.Hour)
	m.cfg.settings.Fade = true
	m.sig.fading = false
	if cmd := m.scheduleFade(); cmd != nil {
		t.Fatal("a fade frame under the idle view")
	}
	if !m.noticeHeld() {
		t.Fatal("a notice would run its time under the idle view")
	}
}
