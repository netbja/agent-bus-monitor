package screen

import (
	"strconv"
	"strings"
)

// render is a first paint of the visible screen for an outer terminal (an
// attaching viewer): styles reset, screen cleared, each line at its row in
// its canonical styles, then the cursor at its place, shown or hidden.
func render(s Snapshot, m Modes) []byte {
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[H\x1b[2J")
	for i, cells := range s.Lines { // the visible rows, never scrollback
		l := ansiLine(cells)
		if l == "" {
			continue
		}
		b.WriteString("\x1b[" + strconv.Itoa(i+1) + ";1H" + l)
	}
	b.WriteString("\x1b[0m\x1b[" + strconv.Itoa(s.CursorY+1) + ";" + strconv.Itoa(s.CursorX+1) + "H")
	if m.CursorHidden {
		b.WriteString("\x1b[?25l")
	} else {
		b.WriteString("\x1b[?25h")
	}
	return []byte(b.String())
}
