package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// pausePopup asks before a send that would wake an agent while the project is
// paused (spec docs/superpowers/specs/2026-10-23-pause-popup-design.md):
// Resume now, Send anyway, Cancel. It sits on its own top page, above the
// line, the compose window and the ask panel, and replays the very send that
// asked through its caller's send path.
type pausePopup struct {
	*tview.Box
	u     *ui
	open  bool
	rec   bus.PauseRecord
	sel   int // 0 Resume now, 1 Send anyway, 2 Cancel (the default)
	key   string
	retry func()
	prev  tview.Primitive
	btns  [3]Rect // the buttons as last drawn, for the mouse
}

var pauseButtons = [3]string{"Resume now", "Send anyway", "Cancel"}

func newPausePopup(u *ui) *pausePopup {
	return &pausePopup{Box: tview.NewBox(), u: u, sel: 2}
}

// pauseGate opens the popup when a waking send meets a running pause, and
// reports whether it did (the caller stops; the popup replays retry). A
// failed pause read, a pause already over, or a send the person already
// chose to make (key) passes.
func (u *ui) pauseGate(key string, retry func()) bool {
	if u.pauseAck == key {
		return false
	}
	u.st.mu.Lock()
	p, failed := u.st.pause, u.st.pauseFailed
	u.st.mu.Unlock()
	if failed || p == nil || !time.Now().Before(p.UntilTime()) {
		return false
	}
	pp := u.pp
	pp.open, pp.rec, pp.key, pp.retry, pp.sel = true, *p, key, retry, 2
	pp.prev = u.app.GetFocus()
	u.pages.ShowPage("pause")
	u.app.SetFocus(pp)
	return true
}

// pauseNote is what a waking send adds to its flash when the pause could not
// be read: unknown, never presented as "not paused".
func (u *ui) pauseNote() string {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	if u.st.pauseFailed {
		return " " + GlyphNone + " pause ?"
	}
	return ""
}

func (u *ui) closePausePopup() {
	pp := u.pp
	pp.open, pp.retry = false, nil
	u.pages.HidePage("pause")
	if pp.prev != nil {
		u.app.SetFocus(pp.prev)
	}
}

// pressPause performs a button.
func (u *ui) pressPause(i int) {
	pp := u.pp
	key, retry := pp.key, pp.retry
	switch i {
	case 0:
		ctx, cancel := context.WithTimeout(u.ctx, pollBudget)
		defer cancel()
		if _, err := u.w.Resume(ctx, u.self, "Resumed from busmon by "+u.self+"."); err != nil {
			u.closePausePopup()
			u.flash, u.flashErr = "resume failed, nothing sent: "+Preview(err.Error()), true
			return
		}
		u.st.mu.Lock()
		u.st.pause = nil
		u.st.mu.Unlock()
		fallthrough
	case 1:
		u.pauseAck = key
		u.closePausePopup()
		if retry != nil {
			retry()
		}
	default:
		u.closePausePopup()
		u.flash, u.flashErr = "not sent: the project is paused", false
	}
}

// pauseKey handles the keyboard while the popup is open; Ctrl-C still quits.
func (u *ui) pauseKey(ev *tcell.EventKey) *tcell.EventKey {
	pp := u.pp
	switch {
	case ev.Key() == tcell.KeyCtrlC:
		return ev
	case ev.Key() == tcell.KeyEscape:
		u.pressPause(2)
	case ev.Key() == tcell.KeyEnter:
		u.pressPause(pp.sel)
	case ev.Key() == tcell.KeyLeft || ev.Key() == tcell.KeyBacktab:
		pp.sel = (pp.sel + 2) % 3
	case ev.Key() == tcell.KeyRight || ev.Key() == tcell.KeyTab:
		pp.sel = (pp.sel + 1) % 3
	case ev.Key() == tcell.KeyRune:
		switch ev.Rune() {
		case 'r', 'R':
			u.pressPause(0)
		case 's', 'S':
			u.pressPause(1)
		case 'c', 'C':
			u.pressPause(2)
		}
	}
	return nil
}

// buttonAt is where button i was last drawn (for the mouse and its tests).
func (pp *pausePopup) buttonAt(i int) (x, y int, ok bool) {
	b := pp.btns[i]
	return b.X, b.Y, b.W > 0
}

// pauseRect is the popup's place on a w×h screen below a header of headerH
// lines: up to 7 rows, centred, never over the header.
func pauseRect(w, h, headerH int) Rect {
	bw := min(72, w-2)
	bh := min(7, h-headerH)
	return Rect{(w - bw) / 2, max(headerH, (h-bh)/2), bw, bh}
}

func (pp *pausePopup) Draw(screen tcell.Screen) {
	if !pp.open {
		return
	}
	sx, sy, sw, sh := pp.GetRect()
	r := pauseRect(sw, sh, pp.u.layout.Header.H)
	if r.W < 20 || r.H < 3 {
		return
	}
	x, y := sx+r.X, sy+r.Y
	for dy := 0; dy < r.H; dy++ { // the popup covers what is under it
		DrawLine(screen, x, y+dy, r.W, nil, false)
	}
	DrawFrame(screen, x, y, r.W, r.H, Frame{Title: Line{{Text: "Project paused", Role: RoleText}}, Focused: true, Hue: RoleAttention})
	iw := r.W - 4
	until := pp.rec.UntilTime().Local().Format("15:04")
	if pp.rec.UntilTime().Sub(time.Now()) > 20*time.Hour {
		until = pp.rec.UntilTime().Local().Format("Jan 2 15:04")
	}
	why := ""
	switch {
	case pp.rec.Reason != "" && pp.rec.By != "":
		why = fmt.Sprintf(" (%s, by %s)", pp.rec.Reason, pp.rec.By)
	case pp.rec.Reason != "":
		why = " (" + pp.rec.Reason + ")"
	case pp.rec.By != "":
		why = " (by " + pp.rec.By + ")"
	}
	// The inner rows, the most needed kept first: buttons, then the message,
	// the hints and the note as room allows (a small screen drops the rest).
	inner := r.H - 2
	msg := Line{{Text: Preview("Project paused until " + until + why + "."), Role: RoleText}}
	note := Line{{Text: "Nothing wakes the agents until it ends.", Role: RoleMuted}}
	hints := Line{{Text: "r resume " + GlyphNone + " s send anyway " + GlyphNone + " c esc cancel", Role: RoleMuted}}
	rows := map[int]Line{}
	btnRow := 0
	switch {
	case inner >= 5:
		rows[0], rows[1], rows[4] = msg, note, hints
		btnRow = 3
	case inner >= 3:
		rows[0], rows[2] = msg, hints
		btnRow = 1
	case inner == 2:
		rows[0] = msg
		btnRow = 1
	}
	for i, l := range rows {
		DrawLine(screen, x+2, y+1+i, iw, l, false)
	}
	bx, by := x+2, y+1+btnRow
	for i, label := range pauseButtons {
		seg := Seg{Text: " " + label + " ", Role: RoleAccent, Pill: i == pp.sel}
		if i == 2 {
			seg.Role = RoleMuted
		}
		w := Line{seg}.Width()
		if bx+w > x+r.W-2 {
			pp.btns[i] = Rect{}
			continue
		}
		DrawLine(screen, bx, by, w, Line{seg}, false)
		pp.btns[i] = Rect{bx, by, w, 1}
		bx += w + 2
	}
}

func (pp *pausePopup) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return pp.WrapInputHandler(func(ev *tcell.EventKey, _ func(p tview.Primitive)) {
		pp.u.pauseKey(ev)
	})
}

// MouseHandler presses a clicked button; every click is the popup's while it
// is open (it is modal).
func (pp *pausePopup) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return pp.WrapMouseHandler(func(action tview.MouseAction, ev *tcell.EventMouse, _ func(p tview.Primitive)) (bool, tview.Primitive) {
		if !pp.open {
			return false, nil
		}
		if action == tview.MouseLeftClick {
			mx, my := ev.Position()
			for i, b := range pp.btns {
				if b.W > 0 && my == b.Y && mx >= b.X && mx < b.X+b.W {
					pp.u.pressPause(i)
					break
				}
			}
		}
		return true, nil
	})
}
