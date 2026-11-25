package main

import (
	tea "charm.land/bubbletea/v2"
)

// busmon-confirm-dialog (the person, 2026-11-25): every yes/no question in
// busmon is one dialog, with one look and one set of keys. A solid box with a
// cell of padding over a dimmed screen; the question centred, optional detail
// lines, two buttons on one line, the safe one (No) focused by default, the
// focused one drawn as a pill; a hint line with the shortcuts. Left, Right
// and Tab move the focus, Enter activates the focused button, y answers yes,
// n and Esc no; any other key leaves it open. A click on a button activates
// it, a click outside the box answers no.

// confirmDialog is one open question. OnYes and OnNo run when it is answered
// (the dialog is closed first, so either may open another); nil does nothing.
type confirmDialog struct {
	Title    string
	Body     []string // detail lines under the question
	Yes, No  string   // the buttons' words: Quit / Stay, Run / Cancel...
	Hue      Role     // the frame's colour (RoleAttention when unset)
	OnYes    func(m *Model) tea.Cmd
	OnNo     func(m *Model) tea.Cmd
	focusYes bool
}

// dimFade is how much of its colour the screen behind an open dialog keeps.
const dimFade = 40

// openConfirm opens d, the safe button focused.
func (m *Model) openConfirm(d *confirmDialog) {
	d.focusYes = false
	if d.Hue == RoleText {
		d.Hue = RoleAttention
	}
	m.ui.dialog = d
}

// answerDialog closes the dialog and runs its answer.
func (m *Model) answerDialog(yes bool) tea.Cmd {
	d := m.ui.dialog
	m.ui.dialog = nil
	if d == nil {
		return nil
	}
	if yes && d.OnYes != nil {
		return d.OnYes(m)
	}
	if !yes && d.OnNo != nil {
		return d.OnNo(m)
	}
	return nil
}

// dialogKey answers the open dialog from a key.
func (m *Model) dialogKey(k Key) tea.Cmd {
	d := m.ui.dialog
	switch {
	case k.Code == keyLeft, k.Code == keyRight, k.Code == keyTab, k.Code == keyBacktab:
		d.focusYes = !d.focusYes
	case k.Code == keyEnter:
		return m.answerDialog(d.focusYes)
	case k.Code == keyRune && (k.Rune == 'y' || k.Rune == 'Y'):
		return m.answerDialog(true)
	case k.Code == keyRune && (k.Rune == 'n' || k.Rune == 'N'), k.Code == keyEscape:
		return m.answerDialog(false)
	}
	return nil
}

// dialogClick answers the open dialog from a click: a button activates, the
// box off the buttons does nothing, outside the box is no.
func (m *Model) dialogClick(x, y int) tea.Cmd {
	box, yes, no := m.ui.dialog.layout(m.width, m.height)
	switch {
	case yes.contains(x, y):
		return m.answerDialog(true)
	case no.contains(x, y):
		return m.answerDialog(false)
	case box.contains(x, y):
		return nil
	}
	return m.answerDialog(false)
}

// buttonText is a button as drawn: "[ Word ]".
func buttonText(word string) string { return "[ " + word + " ]" }

const dialogHint = "y / n " + GlyphNone + " esc"

// layout places the box (centred, padding of two cells across and one down)
// and its two buttons on one line, Yes first.
func (d *confirmDialog) layout(w, h int) (box, yes, no Rect) {
	yw, nw := Width(buttonText(d.Yes)), Width(buttonText(d.No))
	inner := max(Width(d.Title), yw+2+nw, Width(dialogHint))
	for _, l := range d.Body {
		inner = max(inner, Width(l))
	}
	bw := min(inner+6, max(0, w-2)) // borders and two cells of padding each side
	bh := min(8+len(d.Body), max(0, h))
	box = Rect{(w - bw) / 2, max(0, (h-bh)/2), bw, bh}
	row := box.Y + 4 + len(d.Body) // border, padding, title, body, a blank line
	x := box.X + (box.W-(yw+2+nw))/2
	return box, Rect{x, row, yw, 1}, Rect{x + yw + 2, row, nw, 1}
}

// drawConfirmDialog draws d over g: the screen behind dimmed, the box filled.
func drawConfirmDialog(g *grid, d *confirmDialog, w, h int) {
	box, yes, no := d.layout(w, h)
	if box.W < 12 || box.H < 5 {
		return
	}
	for y := 0; y < g.h; y++ { // dim what is behind
		for x := 0; x < g.w; x++ {
			if !box.contains(x, y) {
				c := &g.cells[y*g.w+x]
				if c.st.Fade == 0 || c.st.Fade > dimFade {
					c.st.Fade = dimFade
				}
			}
		}
	}
	for y := box.Y; y < box.Y+box.H; y++ { // the solid fill
		for x := box.X; x < box.X+box.W; x++ {
			g.Put(x, y, " ", cellStyle{Role: RoleText, Surface: true})
		}
	}
	drawFrame(g, box.X, box.Y, box.W, box.H, Frame{Focused: true, Hue: d.Hue})
	centred := func(y int, l Line) {
		if y > box.Y && y < box.Y+box.H-1 {
			lw := 0
			for _, s := range l {
				lw += Width(s.Text)
			}
			drawLine(g, box.X+max(1, (box.W-lw)/2), y, min(lw, box.W-2), l, false)
		}
	}
	centred(box.Y+2, Line{{Text: d.Title, Role: RoleText, Bold: true}})
	for i, l := range d.Body {
		centred(box.Y+3+i, Line{{Text: l, Role: RoleMuted}})
	}
	button := func(r Rect, word string, focused bool) {
		seg := Seg{Text: buttonText(word), Role: RoleText}
		if focused {
			seg = Seg{Text: buttonText(word), Role: RoleAccent, Pill: true}
		}
		drawLine(g, r.X, r.Y, r.W, Line{seg}, false)
	}
	if yes.Y < box.Y+box.H-1 {
		button(yes, d.Yes, d.focusYes)
		button(no, d.No, !d.focusYes)
	}
	centred(yes.Y+1, Line{{Text: dialogHint, Role: RoleMuted}})
	for y := box.Y + 1; y < box.Y+box.H-1; y++ { // the text drawn over the fill keeps its surface
		for x := box.X + 1; x < box.X+box.W-1; x++ {
			if c := &g.cells[y*g.w+x]; !c.st.Pill {
				c.st.Surface = true
			}
		}
	}
}
