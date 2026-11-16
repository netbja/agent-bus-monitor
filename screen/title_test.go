package screen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
)

// The field failure of 2026-11-16 (vt-osc-title): Claude Code sets the
// terminal title at each turn's end, OSC 0 "✳ <project>:<agent>". x/vt
// reads a byte 0x80-0x9F inside a string as an 8-bit C1 control (✳ is
// E2 9C B3: 0x9C is ST), ends the OSC in the middle of the glyph and prints
// the rest on the input line, so Claude's ready signature failed and the
// dispatcher deferred "agent unknown" until someone attached.
//
// claudeReadyWithTitle is Claude's captured ready screen with seq written
// right after its prompt, as the turn's end writes it.
func claudeReadyWithTitle(t *testing.T, seq string) (stream, captured string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agentstate", "testdata", "screens", "claude-empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	captured = string(b)
	i := strings.Index(captured, "❯")
	if i < 0 {
		t.Fatal("no prompt in the capture")
	}
	i += len("❯")
	return replayable(captured[:i] + seq + captured[i:]), captured
}

// replay writes stream into a screen sized for captured, cut at the given
// offsets (each piece its own Write).
func replay(captured, stream string, cuts ...int) Snapshot {
	cols, rows := fixtureSize(captured)
	s := New(cols, rows, 0)
	defer s.Close()
	prev := 0
	for _, c := range append(cuts, len(stream)) {
		s.Write([]byte(stream[prev:c]))
		prev = c
	}
	return s.Snapshot(0)
}

func TestClaudeTitleLeavesTheReadyScreen(t *testing.T) {
	sig := agentstate.Signatures()["claude"]
	const title = "✳ agent-bus-monitor:master"
	for _, seq := range []string{
		"\x1b]0;" + title + "\x07",   // BEL
		"\x1b]0;" + title + "\x1b\\", // ST
		"\x1b]2;" + title + "\x07",
		"\x1b]1;" + title + "\x1b\\",
	} {
		stream, captured := claudeReadyWithTitle(t, seq)
		want := judge(sig, captured)
		at := strings.Index(stream, "\x1b]")
		// whole, then cut at every byte of the sequence and its neighbours
		cuts := [][]int{nil}
		for c := at - 1; c <= at+len(seq)+1; c++ {
			cuts = append(cuts, []int{c})
		}
		cuts = append(cuts, []int{at + 1, at + 3, at + 5, at + 7}) // many Writes inside it
		for _, cut := range cuts {
			snap := replay(captured, stream, cut...)
			plain := snap.Plain()
			if strings.Contains(plain, "agent-bus-monitor:master") {
				t.Fatalf("%q cut %v: the title is on the screen:\n%s", seq, cut, plain)
			}
			replayed := plain
			if sig.ANSI { // Claude's signature reads styles (TestCapturedScreensJudgeTheSame)
				replayed = snap.ANSI()
			}
			if got := judge(sig, replayed); got != want {
				t.Fatalf("%q cut %v: judged %v, the capture %v", seq, cut, got, want)
			}
		}
	}
}

// The title across x/vt's own write chunk (writeChunk): one Write whose
// title straddles the 4096-byte boundary.
func TestTitleAcrossTheWriteChunk(t *testing.T) {
	seq := "\x1b]0;✳ agent-bus-monitor:coder\x07"
	pad := strings.Repeat("x", writeChunk-10)
	s := New(80, 4, 0)
	defer s.Close()
	s.Write([]byte("\x1b[H" + pad[:70] + "\r\n" + pad[70:] + seq + "end"))
	if plain := s.Snapshot(0).Plain(); strings.Contains(plain, "agent-bus-monitor:coder") || !strings.Contains(plain, "end") {
		t.Fatalf("across the chunk:\n%s", plain)
	}
}

// A test per client glyph: the titles and notifications the clients write,
// with glyphs whose UTF-8 holds a C1 byte or not; other string sequences
// with such bytes (OSC 9 and 777 notifications, APC) are dropped whole, and
// text written before and after stays.
func TestStringSequencesNeverPrint(t *testing.T) {
	for _, seq := range []string{
		"\x1b]0;✳ p:claude\x07",       // Claude Code, turn end
		"\x1b]0;✶ p:claude\x07",       // Claude Code's spinner glyphs
		"\x1b]0;✻ p:claude\x1b\\",
		"\x1b]0;⠋ p:codex\x07",        // a braille spinner (E2 A0 8B)
		"\x1b]2;● p:kimi\x07",
		"\x1b]0;🤖 p:grok\x07",
		"\x1b]0;· p:agy\x07",          // U+00B7: no C1 byte, still a title
		"\x1b]0;plain ascii title\x07",
		"\x1b]9;✳ Claude is waiting for your input\x07",
		"\x1b]777;notify;✳ Claude;waiting\x07",
		"\x1b_G✳\x1b\\",
		"\x1bP✳\x1b\\",
	} {
		s := New(60, 2, 0)
		s.Write([]byte("> " + seq + "|"))
		got := strings.TrimRight(s.Snapshot(0).Plain(), " \n")
		s.Close()
		if got != "> |" {
			t.Errorf("%q printed: %q", seq, got)
		}
	}
}

// What the screen needs still reaches it: an ASCII OSC that x/vt acts on
// (a hyperlink's text), a query after a dropped title (answered), and an
// unterminated string interrupted by a new escape sequence.
func TestOtherStringsStillReachTheEmulator(t *testing.T) {
	s := New(40, 2, 0)
	defer s.Close()
	s.Write([]byte("\x1b]8;;http://x/\x07link\x1b]8;;\x07\x1b]0;✳ t\x07\x1b[6n"))
	if got := strings.TrimRight(s.Snapshot(0).Plain(), " \n"); got != "link" {
		t.Fatalf("hyperlink text: %q", got)
	}
	var got string // the query after a dropped title is still answered
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && !strings.Contains(got, "R"); time.Sleep(5 * time.Millisecond) {
		got += string(s.Replies())
	}
	if !strings.Contains(got, "\x1b[1;5R") {
		t.Fatalf("replies %q", got)
	}
	u := New(40, 2, 0)
	defer u.Close()
	u.Write([]byte("a\x1b]0;✳ unterminated\x1b[1mb"))
	if got := strings.TrimRight(u.Snapshot(0).Plain(), " \n"); got != "ab" {
		t.Fatalf("an unterminated title then CSI: %q", got)
	}
}
