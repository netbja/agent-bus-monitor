package main

import tea "charm.land/bubbletea/v2"

// q asks before busmon quits (busmon-quit-confirm, the person): a popup, y
// quits; n, Enter or Esc stay; another key leaves it open. Ctrl-C quits at
// once (key), and display.confirm_quit off makes q quit at once too.

// askQuit is q: the popup, or the quit when the setting is off.
func (m *Model) askQuit() tea.Cmd {
	if !m.cfg.settings.ConfirmQuit {
		return tea.Quit
	}
	m.ui.quitAsk = true
	return nil
}

// quitKey answers the popup.
func (m *Model) quitKey(k Key) tea.Cmd {
	switch {
	case k.Code == keyRune && (k.Rune == 'y' || k.Rune == 'Y'):
		m.ui.quitAsk = false
		return tea.Quit
	case k.Code == keyRune && (k.Rune == 'n' || k.Rune == 'N'), k.Code == keyEnter, k.Code == keyEscape:
		m.ui.quitAsk = false
	}
	return nil
}

// quitRect is the popup's place: centred, 44 wide at most, 5 high.
func quitRect(w, h int) Rect {
	bw, bh := min(44, w-2), min(5, h)
	return Rect{(w - bw) / 2, max(0, (h-bh)/2), bw, bh}
}

// drawQuitPopup draws the question over what is under it.
func drawQuitPopup(screen cellSink, r Rect) {
	if r.W < 20 || r.H < 3 {
		return
	}
	for dy := 0; dy < r.H; dy++ {
		drawLine(screen, r.X, r.Y+dy, r.W, nil, false)
	}
	drawFrame(screen, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: "Quit busmon?", Role: RoleText}}, Focused: true, Hue: RoleAttention})
	iw := r.W - 4
	drawLine(screen, r.X+2, r.Y+1, iw, Line{{Text: "y", Role: RoleAccent}, {Text: " quits", Role: RoleText}}, false)
	if r.H >= 5 {
		drawLine(screen, r.X+2, r.Y+3, iw, Line{{Text: "n, enter or esc stay", Role: RoleMuted}}, false)
	}
}
