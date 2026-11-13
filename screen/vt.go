package screen

import (
	"image/color"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// vtScreen is the production adapter: charmbracelet/x/vt (BAKEOFF.md).
//
// x/vt writes its answers to the program's queries to an unbuffered pipe: a
// Write that triggers one blocks until the pipe is read, so a goroutine
// drains it into replies for the holder. x/vt exposes the alternate screen
// and the cursor's visibility, not bracketed paste nor synchronized output:
// a mode tracker on the same stream follows those (modes.go).
//
// x/vt keeps its scrollback as cells, about 12 KiB a 120-column line, and
// trims it by copying the whole slice: after each write the lines it
// scrolled off move here as rendered ANSI text, into a ring of the asked
// size, and its own scrollback is cleared (BAKEOFF.md). Clear keeps the
// backing array, so writes go to x/vt in writeChunk pieces.
type vtScreen struct {
	mu      sync.Mutex
	e       *vt.Emulator
	modes   modeTracker
	ring    []string // scrollback, oldest at head
	head    int
	keep    int
	replyMu sync.Mutex
	replies []byte
	done    chan struct{}
}

func newVT(cols, rows, scrollback int) *vtScreen {
	s := &vtScreen{e: vt.NewEmulator(max(cols, 1), max(rows, 1)), keep: max(scrollback, 0), done: make(chan struct{})}
	if alt := altScreenOf(s.e); alt != nil { // the alternate screen keeps no scrollback (vtalt.go)
		alt.SetScrollbackSize(1)
	}
	go func() {
		defer close(s.done)
		buf := make([]byte, 4096)
		for {
			n, err := s.e.Read(buf)
			if n > 0 {
				s.replyMu.Lock()
				if len(s.replies) < 64<<10 { // a program that floods queries gets no more answers, never a stalled screen
					s.replies = append(s.replies, buf[:n]...)
				}
				s.replyMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *vtScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modes.feed(p)
	n := 0
	for len(p) > 0 { // x/vt's Clear keeps the backing array of every line one write scrolled: a bounded write bounds it
		chunk := p[:min(len(p), writeChunk)]
		m, err := s.e.Write(chunk)
		n += m
		s.takeScrollback()
		if err != nil {
			return n, err
		}
		p = p[len(chunk):]
	}
	return n, nil
}

// writeChunk bounds what one x/vt write may scroll off: its scrollback keeps
// the array of the lines one write pushed (about 110 bytes a cell of
// content), however small the ring asked for.
const writeChunk = 4096

// takeScrollback moves the lines x/vt scrolled off into the ring.
func (s *vtScreen) takeScrollback() {
	n := s.e.ScrollbackLen()
	if n == 0 {
		return
	}
	if s.keep > 0 {
		cols := s.e.Width()
		for y := max(0, n-s.keep); y < n; y++ {
			line := make([]Cell, cols)
			for x := range line {
				line[x] = fromUV(s.e.ScrollbackCellAt(x, y))
			}
			s.push(ansiLine(line))
		}
	}
	s.e.ClearScrollback()
}

// push adds a line, dropping the oldest once the ring is full.
func (s *vtScreen) push(line string) {
	if len(s.ring) < s.keep {
		s.ring = append(s.ring, line)
		return
	}
	s.ring[s.head] = line
	s.head = (s.head + 1) % s.keep
}

func (s *vtScreen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.e.Resize(max(cols, 1), max(rows, 1))
}

func (s *vtScreen) Modes() Modes {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.modes.modes
	m.AltScreen = s.e.IsAltScreen()
	m.CursorHidden = s.e.CursorHidden()
	return m
}

func (s *vtScreen) Replies() []byte {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	out := s.replies
	s.replies = nil
	return out
}

func (s *vtScreen) Close() error {
	err := s.e.Close()
	<-s.done
	return err
}

func (s *vtScreen) Snapshot(lines int) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	cols, rows := s.e.Width(), s.e.Height()
	snap := Snapshot{Cols: cols, Rows: rows}
	if lines > 0 && !s.e.IsAltScreen() {
		n := len(s.ring)
		for i := max(0, n-lines); i < n; i++ {
			snap.Scrollback = append(snap.Scrollback, s.ring[(s.head+i)%n])
		}
	}
	for y := 0; y < rows; y++ {
		line := make([]Cell, cols)
		for x := range line {
			line[x] = fromUV(s.e.CellAt(x, y))
		}
		snap.Lines = append(snap.Lines, line)
	}
	p := s.e.CursorPosition()
	snap.CursorX, snap.CursorY = p.X, p.Y
	return snap
}

func (s *vtScreen) Render() []byte {
	snap := s.Snapshot(0)
	return render(snap, s.Modes())
}

// fromUV is one x/vt cell in the seam's model. A nil cell (outside the
// screen, or an empty scrollback slot) is a blank.
func fromUV(c *uv.Cell) Cell {
	if c == nil {
		return Cell{Width: 1}
	}
	out := Cell{Text: c.Content, Width: c.Width}
	st := c.Style
	out.Attr = Attr{
		Bold: st.Attrs&uv.AttrBold != 0, Dim: st.Attrs&uv.AttrFaint != 0, Italic: st.Attrs&uv.AttrItalic != 0,
		Underline: st.Underline != 0, Reverse: st.Attrs&uv.AttrReverse != 0,
		FG: fromColor(st.Fg), BG: fromColor(st.Bg),
	}
	return out
}

// fromColor maps x/ansi's colors to the seam's.
func fromColor(c color.Color) Color {
	switch v := c.(type) {
	case nil:
		return Color{}
	case ansi.BasicColor:
		return Color{Kind: ColorBasic, N: uint8(v)}
	case ansi.IndexedColor:
		return Color{Kind: ColorIndexed, N: uint8(v)}
	}
	r, g, b, _ := c.RGBA()
	return Color{Kind: ColorRGB, R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}
}
