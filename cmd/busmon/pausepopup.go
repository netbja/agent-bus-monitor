package main

import (
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var pauseButtons = [3]string{"Resume now", "Send anyway", "Cancel"}

// pauseRect is the popup's place on a w×h screen below a header of headerH
// lines: up to 7 rows, centred, never over the header.
func pauseRect(w, h, headerH int) Rect {
	bw := min(72, w-2)
	bh := min(7, h-headerH)
	return Rect{(w - bw) / 2, max(headerH, (h-bh)/2), bw, bh}
}

// drawPausePopup draws the popup in r on any cell sink (the
// shell's grid) and returns where each button went (zero: not drawn).
func drawPausePopup(screen cellSink, r Rect, rec bus.PauseRecord, sel int, now time.Time) (btns [3]Rect) {
	if r.W < 20 || r.H < 3 {
		return btns
	}
	x, y := r.X, r.Y
	for dy := 0; dy < r.H; dy++ { // the popup covers what is under it
		drawLine(screen, x, y+dy, r.W, nil, false)
	}
	drawFrame(screen, x, y, r.W, r.H, Frame{Title: Line{{Text: "Project paused", Role: RoleText}}, Focused: true, Hue: RoleAttention})
	iw := r.W - 4
	until := rec.UntilTime().Local().Format("15:04")
	if rec.UntilTime().Sub(now) > 20*time.Hour {
		until = rec.UntilTime().Local().Format("Jan 2 15:04")
	}
	why := ""
	switch {
	case rec.Reason != "" && rec.By != "":
		why = fmt.Sprintf(" (%s, by %s)", rec.Reason, rec.By)
	case rec.Reason != "":
		why = " (" + rec.Reason + ")"
	case rec.By != "":
		why = " (by " + rec.By + ")"
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
		drawLine(screen, x+2, y+1+i, iw, l, false)
	}
	bx, by := x+2, y+1+btnRow
	for i, label := range pauseButtons {
		seg := Seg{Text: " " + label + " ", Role: RoleAccent, Pill: i == sel}
		if i == 2 {
			seg.Role = RoleMuted
		}
		w := Line{seg}.Width()
		if bx+w > x+r.W-2 {
			continue
		}
		drawLine(screen, bx, by, w, Line{seg}, false)
		btns[i] = Rect{bx, by, w, 1}
		bx += w + 2
	}
	return btns
}
