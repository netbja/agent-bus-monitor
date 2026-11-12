package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// The marquee (the person's idea, 2026-11-12): on the Work tab, a focused
// row or card whose text is cut with … scrolls it once, a cell a frame, holds
// the end, then shows the cut text again and stops. Cheap by construction:
// frames are scheduled only while the focused Work panel's selected row is
// cut and its pass is not over; nothing runs otherwise. The Kanban scrolls
// each cut line of the selected card; the list scrolls the row's title field
// in place (ListItem.Marquee), the columns after it unmoved.

const (
	marqueeDelay = 900 * time.Millisecond // before the pass: walking the rows starts nothing
	marqueeFrame = 90 * time.Millisecond  // one cell
	marqueeHold  = 1500 * time.Millisecond
)

// marquee is the Work list's pass: the row, its offset in cells, and the
// serial its frames carry (a frame of an older pass does nothing).
type marquee struct {
	key    string
	off    int
	serial int
	done   bool
}

// marqueeMsg is one frame of the pass serial.
type marqueeMsg struct{ serial int }

// rowMarquee is a list row's cut field: x cells from the row's start, w
// cells wide, and its whole text.
type rowMarquee struct {
	x, w int
	text Line
}

// fieldMarquee is the field's marquee when text does not fit in w cells.
func fieldMarquee(x, w int, text Line) *rowMarquee {
	if w <= 0 || text.Width() <= w {
		return nil
	}
	return &rowMarquee{x: x, w: w, text: text}
}

// dropCells is l without its first n cells; half a wide cluster is a space.
func dropCells(l Line, n int) Line {
	var out Line
	skip := n
	lineClusters(l, func(text string, width int, style Seg) bool {
		switch {
		case skip <= 0:
			out = appendSegment(out, text, style)
		case width > skip:
			out = appendSegment(out, " ", style)
			skip = 0
		default:
			skip -= width
		}
		return true
	})
	return out
}

// scrolled is l moved left by off cells, never past its end in w cells.
func scrolled(l Line, w, off int) Line {
	return dropCells(l, min(off, max(0, l.Width()-w)))
}

// marqueeOver is how far the row key can scroll: its longest overflow, 0
// when it fits or is not drawn.
func (l *listState) marqueeOver(key string) int {
	if key == "" {
		return 0
	}
	if k := l.kanban; k != nil {
		x, y, w, h := l.inner()
		rects := kanbanGrid(len(k.cols), x, y, w, h)
		for c, col := range k.cols {
			for _, card := range col.cards {
				if card.key != key {
					continue
				}
				over := 0
				for _, ln := range card.lines {
					over = max(over, ln.Width()-rects[c].w)
				}
				return max(over, 0)
			}
		}
		return 0
	}
	if i := l.index(key); i >= 0 {
		if mq := l.items[i].Marquee; mq != nil {
			return max(0, mq.text.Width()-mq.w)
		}
	}
	return 0
}

// marqueeOn is the row the pass may run on: the Work panel shown and
// focused in the shell, its selected row cut; "" otherwise.
func (m *Model) marqueeOn() string {
	l := &m.lists.work
	if m.ui.idle || m.mode() != ModeShell || m.ui.focus != PanelWork || m.layout.Active != PanelWork {
		return ""
	}
	if key := l.Selected(); l.marqueeOver(key) > 0 {
		return key
	}
	return ""
}

// armMarquee starts a pass when the row changes (after every update): a
// new serial, offset 0, the first frame after marqueeDelay. Leaving the row
// stops the pass and shows the cut text again.
func (m *Model) armMarquee() tea.Cmd {
	l := &m.lists.work
	key := m.marqueeOn()
	if key == l.mq.key {
		return nil
	}
	l.mq = marquee{key: key, serial: l.mq.serial + 1}
	if key == "" {
		return nil
	}
	return marqueeTick(marqueeDelay, l.mq.serial)
}

func marqueeTick(d time.Duration, serial int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return marqueeMsg{serial: serial} })
}

// marqueeStep is one frame: a cell further, the hold at the end, then back
// to the cut text, done.
func (m *Model) marqueeStep(msg marqueeMsg) tea.Cmd {
	mq := &m.lists.work.mq
	if msg.serial != mq.serial || mq.key == "" || mq.done {
		return nil
	}
	over := m.lists.work.marqueeOver(mq.key)
	switch {
	case mq.off < over:
		mq.off++
		if mq.off == over {
			return marqueeTick(marqueeHold, mq.serial)
		}
		return marqueeTick(marqueeFrame, mq.serial)
	default: // the hold is over, or the row no longer overflows
		mq.off, mq.done = 0, true
		return nil
	}
}
