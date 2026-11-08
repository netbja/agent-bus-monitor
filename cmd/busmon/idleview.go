package main

import (
	tea "charm.land/bubbletea/v2"
)

// The idle view (spec docs/superpowers/specs/2026-11-08-busmon-idle-view-design.md,
// layout A chosen by the person): after display.idle_view_after without input,
// a calm screen replaces the dashboard; the first input only wakes it.

// isInput is the person: a key, a paste, a click, the wheel, a motion, or
// coming back to the window. A tick, a tail batch, a resize, a fade frame, a
// blur or a release is not.
func isInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg, tea.FocusMsg:
		return true
	}
	return false
}

// enterIdle shows the idle view when it is on, its delay has passed since the
// last input, nothing modal is open, and the screen is not too small.
func (m *Model) enterIdle() {
	after := m.cfg.settings.IdleView
	if m.ui.idle || after <= 0 || m.ui.lastInput.IsZero() || m.now.Sub(m.ui.lastInput) < after ||
		m.width == 0 || m.layout.Tier == TierTooSmall || m.busyForModal() {
		return
	}
	m.ui.idle, m.ui.idleSince, m.ui.idleBlurred = true, m.now, false
}

// idleFocus handles the focus reports while the view is shown, and says
// whether it took msg. A terminal reports the current focus as soon as the
// reports are turned on (Ghostty, foureyes), so a focus is the person only
// after a blur: they left the window and came back.
func (m *Model) idleFocus(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.BlurMsg:
		m.ui.idleBlurred = true
		return true
	case tea.FocusMsg:
		return !m.ui.idleBlurred // the initial report: nothing; after a blur, isInput wakes the view
	}
	return false
}

// leaveIdle returns to the dashboard as the person left it.
func (m *Model) leaveIdle() {
	m.ui.idle, m.ui.lastInput = false, m.now
}
