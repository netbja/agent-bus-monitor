//go:build bakeoff

package screen

// The emulator bake-off (spec 2026-11-10-herdr-exit-design.md 6.4), run on
// demand, never in the default suite:
//
//	go test ./screen -tags bakeoff -run TestBakeoff -v
//	go test ./screen -tags bakeoff -run '^$' -bench Bakeoff -benchmem
//	go test ./screen -tags 'bakeoff gostty' ...   (gostty joins the speed table)
//
// Its numbers are in BAKEOFF.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
	"github.com/netbja/agent-bus-monitor/agentstate"
)

// candidate is one emulator behind the seam's model.
type candidate struct {
	name string
	new  func(cols, rows int) Screen
}

var candidates = []candidate{
	{"x/vt", func(c, r int) Screen { return newVT(c, r, 0) }},
	{"vt10x", func(c, r int) Screen { return newVT10x(c, r) }},
}

// vt10xScreen is hinshun/vt10x behind the seam (bake-off only). It has no
// faint attribute, no grapheme clusters (a rune a cell) and no scrollback;
// its modes are its flags.
type vt10xScreen struct{ t vt10x.Terminal }

func newVT10x(cols, rows int) *vt10xScreen {
	return &vt10xScreen{t: vt10x.New(vt10x.WithSize(cols, rows))}
}

func (s *vt10xScreen) Write(p []byte) (int, error) { return s.t.Write(p) }
func (s *vt10xScreen) Resize(c, r int)             { s.t.Resize(c, r) }
func (s *vt10xScreen) Replies() []byte             { return nil }
func (s *vt10xScreen) Close() error                { return nil }
func (s *vt10xScreen) Render() []byte              { return render(s.Snapshot(0), s.Modes()) }
func (s *vt10xScreen) Modes() Modes {
	m := s.t.Mode()
	return Modes{AltScreen: m&vt10x.ModeAltScreen != 0, CursorHidden: !s.t.CursorVisible()}
}

func vt10xColor(c vt10x.Color) Color {
	switch {
	case c >= 1<<24:
		return Color{}
	case c < 16:
		return Color{Kind: ColorBasic, N: uint8(c)}
	case c < 256:
		return Color{Kind: ColorIndexed, N: uint8(c)}
	}
	return Color{Kind: ColorRGB, R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

func (s *vt10xScreen) Snapshot(int) Snapshot {
	s.t.Lock()
	defer s.t.Unlock()
	cols, rows := s.t.Size()
	snap := Snapshot{Cols: cols, Rows: rows}
	for y := 0; y < rows; y++ {
		line := make([]Cell, cols)
		for x := range line {
			g := s.t.Cell(x, y)
			text := string(g.Char)
			if g.Char == 0 {
				text = " "
			}
			line[x] = Cell{Text: text, Width: 1, Attr: Attr{
				Bold: g.Mode&4 != 0, Underline: g.Mode&2 != 0, Reverse: g.Mode&1 != 0, Italic: g.Mode&16 != 0,
				FG: vt10xColor(g.FG), BG: vt10xColor(g.BG)}}
		}
		snap.Lines = append(snap.Lines, line)
	}
	c := s.t.Cursor()
	snap.CursorX, snap.CursorY = c.X, c.Y
	return snap
}

// capturedScreens are agentstate's screen fixtures, by name.
func capturedScreens(t testing.TB) map[string]string {
	dir := filepath.Join("..", "agentstate", "testdata", "screens")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(e.Name(), ".txt")] = string(b)
	}
	return out
}

// TestBakeoff prints each candidate's fidelity: corpus cases equal to the
// oracle, and captured screens agentstate judges the same once replayed.
func TestBakeoff(t *testing.T) {
	sigs := agentstate.Signatures()
	screens := capturedScreens(t)
	names := make([]string, 0, len(screens))
	for n := range screens {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, cand := range candidates {
		corpusOK, judged, judgedOK := 0, 0, 0
		var misses []string
		for _, c := range corpus {
			want, _ := os.ReadFile(filepath.Join("testdata", "oracle", c.name+".txt"))
			s := play(t, cand.new, c)
			if visible(s.Snapshot(0)) == string(want) {
				corpusOK++
			} else {
				misses = append(misses, "corpus:"+c.name)
			}
			s.Close()
		}
		for _, n := range names {
			sig, ok := sigs[fixtureClient(n)]
			if !ok {
				continue
			}
			captured := screens[n]
			cols, rows := fixtureSize(captured)
			s := cand.new(cols, rows)
			s.Write([]byte(replayable(captured)))
			snap := s.Snapshot(0)
			s.Close()
			replayed := snap.ANSI()
			if !sig.ANSI {
				replayed = snap.Plain()
			}
			judged++
			if judge(sig, replayed) == judge(sig, captured) {
				judgedOK++
			} else {
				misses = append(misses, "screen:"+n)
			}
		}
		t.Logf("%-6s corpus %d/%d  captured screens %d/%d  misses %v", cand.name, corpusOK, len(corpus), judgedOK, judged, misses)
	}
}

// stream is the benchmark's input: every corpus case and every captured
// screen, replayed, repeated to about 1 MiB.
func stream(b *testing.B) []byte {
	var one strings.Builder
	for _, c := range corpus {
		one.WriteString(c.data + "\r\n")
	}
	for _, s := range capturedScreens(b) {
		one.WriteString(replayable(s) + "\r\n")
	}
	var all strings.Builder
	for all.Len() < 1<<20 {
		all.WriteString(one.String())
	}
	return []byte(all.String())
}

// BenchmarkBakeoffWrite: throughput (MB/s) and allocations feeding 1 MiB of
// agent-like output to a 120x40 screen, in 4 KiB writes as a PTY reads.
func BenchmarkBakeoffWrite(b *testing.B) {
	data := stream(b)
	for _, cand := range candidates {
		b.Run(cand.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s := cand.new(120, 40)
				for off := 0; off < len(data); off += 4096 {
					s.Write(data[off:min(off+4096, len(data))])
				}
				s.Close()
			}
		})
	}
}

// BenchmarkBakeoffSnapshot: one read of a full 120x40 screen, as the
// dispatcher's observe pass reads it.
func BenchmarkBakeoffSnapshot(b *testing.B) {
	data := stream(b)
	for _, cand := range candidates {
		b.Run(cand.name, func(b *testing.B) {
			s := cand.new(120, 40)
			s.Write(data)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = s.Snapshot(0).ANSI()
			}
		})
	}
}

// TestBakeoffMemory prints the heap one 120x40 screen keeps after 1 MiB of
// output, and x/vt's with a 5 000-line scrollback (holder.scrollback).
func TestBakeoffMemory(t *testing.T) {
	var one strings.Builder
	for one.Len() < 1<<20 {
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&one, "line %d %s\r\n", i, strings.Repeat("x", 100))
		}
	}
	data := []byte(one.String())
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	measure := func(name string, mk func() Screen) {
		before := heap()
		s := mk()
		for off := 0; off < len(data); off += 4096 { // as a PTY reads
			s.Write(data[off:min(off+4096, len(data))])
		}
		after := heap()
		t.Logf("%-22s %7.1f KiB retained", name, float64(int64(after)-int64(before))/1024)
		runtime.KeepAlive(s)
		s.Close()
	}
	for _, cand := range candidates {
		measure(cand.name, func() Screen { return cand.new(120, 40) })
	}
	measure("x/vt +5000 scrollback", func() Screen { return newVT(120, 40, 5000) })
}
