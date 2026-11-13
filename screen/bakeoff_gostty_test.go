//go:build bakeoff && gostty

package screen

import "github.com/ironpark/gostty"

// gostty in the speed table, as the reference (the oracle; never in
// production: a native library, spec 6.2). Its fidelity is the oracle's.
func init() {
	candidates = append(candidates, candidate{"gostty", func(c, r int) Screen { return newGostty(c, r) }})
}

type gosttyScreen struct{ s *gostty.Session }

func newGostty(cols, rows int) *gosttyScreen {
	s, err := gostty.New(uint16(cols), uint16(rows), gostty.WithTerminalOptions(gostty.WithScrollbackMaxLines(0)))
	if err != nil {
		panic(err)
	}
	return &gosttyScreen{s: s}
}

func (g *gosttyScreen) Write(p []byte) (int, error) { return g.s.Write(p) }
func (g *gosttyScreen) Resize(c, r int)             { g.s.Terminal().Resize(uint16(c), uint16(r)) }
func (g *gosttyScreen) Replies() []byte             { return nil }
func (g *gosttyScreen) Close() error                { return g.s.Close() }
func (g *gosttyScreen) Modes() Modes                { return Modes{} }
func (g *gosttyScreen) Render() []byte              { return nil }

// Snapshot: plain text only (the speed table reads it; fidelity is the
// oracle's own).
func (g *gosttyScreen) Snapshot(int) Snapshot {
	out, _ := g.s.Terminal().FormatString(gostty.FormatOptions{Format: gostty.FormatterFormatPlain})
	var snap Snapshot
	for _, l := range splitLines(out) {
		line := []Cell{}
		for _, r := range l {
			line = append(line, Cell{Text: string(r), Width: 1})
		}
		snap.Lines = append(snap.Lines, line)
	}
	return snap
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
