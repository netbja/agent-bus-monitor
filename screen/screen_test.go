package screen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/netbja/agent-bus-monitor/agentstate"
)

// visible is a snapshot as the oracle prints it: no trailing blank lines.
func visible(s Snapshot) string {
	return strings.TrimRight(s.Plain(), "\n")
}

func play(t testing.TB, newScreen func(cols, rows int) Screen, c corpusCase) Screen {
	t.Helper()
	s := newScreen(c.cols, c.rows)
	if _, err := s.Write([]byte(c.data)); err != nil {
		t.Fatal(err)
	}
	return s
}

func production(cols, rows int) Screen { return New(cols, rows, 0) }

// knownDifferences are the corpus cases where the production emulator and
// the oracle differ, each with its reason (BAKEOFF.md); a fixed one fails
// here so the list stays true.
var knownDifferences = map[string]string{
	"wide-at-right-edge": "x/vt drops a wide cluster that starts in the last column instead of wrapping it to the next line",
}

// Every corpus case renders as gostty renders it (the oracle, committed),
// but the known differences.
func TestCorpusMatchesTheOracle(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			if why, ok := knownDifferences[c.name]; ok {
				want, _ := os.ReadFile(filepath.Join("testdata", "oracle", c.name+".txt"))
				s := play(t, production, c)
				defer s.Close()
				if visible(s.Snapshot(0)) == string(want) {
					t.Fatalf("now matches the oracle: drop it from knownDifferences (%s)", why)
				}
				return
			}
			want, err := os.ReadFile(filepath.Join("testdata", "oracle", c.name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			s := play(t, production, c)
			defer s.Close()
			if got := visible(s.Snapshot(0)); got != string(want) {
				t.Fatalf("screen\n%s\n--- oracle\n%s", got, want)
			}
		})
	}
}

// fixtureClient is the profile a captured screen belongs to.
func fixtureClient(name string) string {
	c := strings.SplitN(name, "-", 2)[0]
	if c == "agy153" {
		return "agy"
	}
	return c
}

// judge is everything agentstate decides from one screen.
func judge(sig agentstate.Signature, screen string) [5]any {
	ready, busy := sig.Match(screen)
	blocked, what := sig.BlockedOn(screen)
	return [5]any{ready, busy, sig.InDialog(screen), blocked, what}
}

// Each captured screen, replayed as bytes, reads to agentstate as the
// capture itself does: ready, busy, dialog and blocked alike (spec 6.4:
// an emulator passes when its screen satisfies every signature test).
func TestCapturedScreensJudgeTheSame(t *testing.T) {
	dir := filepath.Join("..", "agentstate", "testdata", "screens")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sigs := agentstate.Signatures()
	n := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".txt")
		sig, ok := sigs[fixtureClient(name)]
		if !ok || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		captured := string(b)
		cols, rows := fixtureSize(captured)
		s := New(cols, rows, 0)
		s.Write([]byte(replayable(captured)))
		snap := s.Snapshot(0)
		s.Close()
		replayed := snap.ANSI()
		if !sig.ANSI {
			replayed = snap.Plain()
		}
		if got, want := judge(sig, replayed), judge(sig, captured); got != want {
			t.Errorf("%s: replayed %v, captured %v\n%s", name, got, want, replayed)
		}
		n++
	}
	if n < 30 {
		t.Fatalf("only %d captured screens replayed", n)
	}
}

// fixtureSize is the screen a capture fits in: its widest line, its lines.
func fixtureSize(captured string) (cols, rows int) {
	lines := strings.Split(strings.ReplaceAll(captured, "\r", ""), "\n")
	for _, l := range lines {
		cols = max(cols, ansi.StringWidth(l))
	}
	return max(cols+2, 20), len(lines) + 1
}

// The modes the holder needs, set and reset in any chunking.
func TestModes(t *testing.T) {
	s := New(20, 4, 0)
	defer s.Close()
	if m := s.Modes(); m != (Modes{}) {
		t.Fatalf("fresh %+v", m)
	}
	for _, chunk := range []string{esc + "[?20", "04h", esc + "[?2026h", esc + "[?25l", esc + "[?1049h"} {
		s.Write([]byte(chunk))
	}
	if m := s.Modes(); !m.BracketedPaste || !m.SyncOutput || !m.CursorHidden || !m.AltScreen {
		t.Fatalf("set %+v", m)
	}
	// Leaving the alternate screen first: x/vt restores the cursor's
	// visibility saved on entering it (xterm keeps it), BAKEOFF.md.
	s.Write([]byte(esc + "[?2004;2026l" + esc + "[?1049l" + esc + "[?25h"))
	if m := s.Modes(); m != (Modes{}) {
		t.Fatalf("reset %+v", m)
	}
	s.Write([]byte(esc + "[2004h")) // an ANSI mode of that number is not the private one
	if s.Modes().BracketedPaste {
		t.Fatal("CSI 2004 h read as ?2004")
	}
}

// A program's query is answered (the holder forwards the reply to the PTY)
// and never stalls the write.
func TestQueriesAreAnsweredWithoutBlocking(t *testing.T) {
	s := New(20, 4, 0)
	defer s.Close()
	done := make(chan struct{})
	go func() {
		s.Write([]byte("ab" + esc + "[6n" + esc + "[c"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a query blocked the write")
	}
	var got string
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && !strings.Contains(got, "R"); time.Sleep(5 * time.Millisecond) {
		got += string(s.Replies())
	}
	if !strings.Contains(got, esc+"[1;3R") {
		t.Fatalf("replies %q", got)
	}
	if r := s.Replies(); len(r) != 0 && strings.Contains(string(r), "R") {
		t.Fatalf("a reply taken twice: %q", r)
	}
}

// Styles come out canonical: the dim placeholder as the captured screens
// carry it, so Claude Code's ready signature matches a replayed screen.
func TestANSIIsCanonical(t *testing.T) {
	s := New(30, 5, 0)
	defer s.Close()
	s.Write([]byte(esc + "[38;2;215;119;87m❯ " + esc + "[0m" + esc + "[2mTry it" + esc + "[0m\r\n" + esc + "[1;31mx" + esc + "[m  \r\n" + "日z\r\n" +
		"in" + esc + "[44m   " + esc + "[0m\r\n" + esc + "[93mb" + esc + "[103mB" + esc + "[0m"))
	lines := strings.Split(s.Snapshot(0).ANSI(), "\n")
	for i, want := range []string{
		"\x1b[0m\x1b[38;2;215;119;87m❯ \x1b[0m\x1b[2mTry it\x1b[0m",
		"\x1b[0m\x1b[1m\x1b[31mx\x1b[0m",
		"日z",
		"in\x1b[0m\x1b[44m   \x1b[0m", // a styled trailing blank is seen: kept (Codex's input box is a background)
		"\x1b[0m\x1b[93mb\x1b[0m\x1b[93m\x1b[103mB\x1b[0m",
	} {
		if lines[i] != want {
			t.Errorf("line %d %q, want %q", i, lines[i], want)
		}
	}
	snap := s.Snapshot(0)
	if c := snap.Lines[2][0]; c.Text != "日" || c.Width != 2 || snap.Lines[2][1].Width != 0 {
		t.Errorf("wide cell %+v %+v", c, snap.Lines[2][1])
	}
}

// Scrollback: Snapshot(n) puts up to n scrolled-off lines above the screen;
// a full-screen program's screen has none.
func TestScrollback(t *testing.T) {
	s := New(10, 2, 100)
	defer s.Close()
	s.Write([]byte("a\r\nb\r\nc\r\nd"))
	if got := s.Snapshot(0).Plain(); got != "c\nd" {
		t.Fatalf("screen %q", got)
	}
	if got := s.Snapshot(1).Plain(); got != "b\nc\nd" {
		t.Fatalf("one line back %q", got)
	}
	if got := s.Snapshot(50).Plain(); got != "a\nb\nc\nd" {
		t.Fatalf("all %q", got)
	}
	s.Write([]byte(esc + "[?1049h"))
	if snap := s.Snapshot(50); len(snap.Scrollback) != 0 || len(snap.Lines) != 2 {
		t.Fatalf("alt screen with scrollback: %d + %d lines", len(snap.Scrollback), len(snap.Lines))
	}
}

// Resize keeps what fits.
func TestResize(t *testing.T) {
	s := New(10, 3, 0)
	defer s.Close()
	s.Write([]byte("hello"))
	s.Resize(20, 5)
	snap := s.Snapshot(0)
	if snap.Cols != 20 || snap.Rows != 5 || !strings.HasPrefix(snap.Plain(), "hello") {
		t.Fatalf("%+v %q", snap.Cols, snap.Plain())
	}
}

// Render paints an empty outer terminal into this screen: cleared, each
// line at its row, the cursor at its place and visibility.
func TestRender(t *testing.T) {
	s := New(20, 3, 10)
	defer s.Close()
	s.Write([]byte("old\r\n\r\n\r\ntop\r\n" + esc + "[2mdim" + esc + "[0m" + esc + "[?25l"))
	r := string(s.Render())
	for _, want := range []string{"\x1b[H\x1b[2J", "\x1b[2;1Htop", "\x1b[3;1H\x1b[0m\x1b[2mdim\x1b[0m", "\x1b[3;4H", "\x1b[?25l"} {
		if !strings.Contains(r, want) {
			t.Errorf("render lacks %q: %q", want, r)
		}
	}
	if strings.Contains(r, "old") {
		t.Errorf("render painted scrollback: %q", r)
	}
}

// The scrollback ring keeps the newest lines, in order, once it wraps, with
// their styles; x/vt's own scrollback stays empty (the cells cost kilobytes
// a line, BAKEOFF.md).
func TestScrollbackRing(t *testing.T) {
	s := newVT(10, 2, 3)
	defer s.Close()
	for i := 0; i < 9; i++ {
		s.Write([]byte(string(rune('a'+i)) + "\r\n"))
	}
	s.Write([]byte(esc + "[2mz" + esc + "[0m"))
	snap := s.Snapshot(10)
	if got := strings.Join(snap.Scrollback, ","); got != "f,g,h" {
		t.Fatalf("ring %q", got)
	}
	if got := snap.Plain(); got != "f\ng\nh\ni\nz" {
		t.Fatalf("plain %q", got)
	}
	if s.e.ScrollbackLen() != 0 {
		t.Fatal("x/vt kept its own scrollback")
	}
	none := newVT(10, 2, 0)
	defer none.Close()
	none.Write([]byte("a\r\nb\r\nc\r\nd"))
	if snap := none.Snapshot(10); len(snap.Scrollback) != 0 || none.e.ScrollbackLen() != 0 {
		t.Fatalf("no scrollback asked, kept %d", len(snap.Scrollback))
	}
	s.Write([]byte(esc + "[2mdimmed" + esc + "[0m" + strings.Repeat("\r\n", 3)))
	if sb := s.Snapshot(3).Scrollback; len(sb) != 3 || !strings.Contains(strings.Join(sb, ","), "\x1b[0m\x1b[2mzdimmed\x1b[0m") {
		t.Fatalf("a styled scrolled line lost its style: %q", sb)
	}
}

// A single large write is fed to x/vt in bounded pieces: the screen comes
// out the same as from small writes, and the retained heap stays small.
func TestLargeWriteIsChunked(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString("a line of output that is long enough to count\r\n")
	}
	big, small := New(80, 10, 50), New(80, 10, 50)
	defer big.Close()
	defer small.Close()
	if n, err := big.Write([]byte(b.String())); err != nil || n != b.Len() {
		t.Fatalf("wrote %d of %d: %v", n, b.Len(), err)
	}
	data := []byte(b.String())
	for off := 0; off < len(data); off += 100 {
		small.Write(data[off:min(off+100, len(data))])
	}
	if big.Snapshot(50).Plain() != small.Snapshot(50).Plain() {
		t.Fatal("a large write renders unlike small ones")
	}
	if big.(*vtScreen).e.ScrollbackLen() != 0 {
		t.Fatal("x/vt kept scrolled lines")
	}
}

// Production stays pure Go (spec 6.2, Q3): nothing the binaries or this
// package build links gostty, its loader or the bake-off's vt10x.
func TestProductionLinksNoOracle(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "../cmd/...", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, bad := range []string{"github.com/ironpark/gostty", "github.com/ebitengine/purego", "github.com/hinshun/vt10x"} {
		if strings.Contains(string(out), bad) {
			t.Errorf("a production build links %s", bad)
		}
	}
}
