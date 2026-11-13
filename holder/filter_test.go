package holder

import (
	"strings"
	"testing"
)

// The output filter (spec 4.6, security S3): on each viewer's stream, the
// sequences that would write the person's clipboard, hide a link's target
// or make the person's terminal type a reply into the program are dropped;
// everything else passes byte for byte, however the stream is cut.
func TestFilterOutput(t *testing.T) {
	const bel, st = "\x07", "\x1b\\"
	cases := []struct {
		name, in, want string
		osc52          bool
	}{
		{"plain text", "hello \x1b[1;31mred\x1b[0m\r\n", "hello \x1b[1;31mred\x1b[0m\r\n", false},
		{"OSC 52 BEL", "a\x1b]52;c;aGVsbG8=" + bel + "b", "ab", false},
		{"OSC 52 ST", "a\x1b]52;c;aGVsbG8=" + st + "b", "ab", false},
		{"OSC 52 allowed by the setting", "a\x1b]52;c;aGVsbG8=" + bel + "b", "a\x1b]52;c;aGVsbG8=" + bel + "b", true},
		{"OSC 8 link: its text kept", "\x1b]8;;https://evil.example" + st + "docs" + "\x1b]8;;" + st, "docs", false},
		{"a title set passes", "\x1b]0;my title" + bel + "x", "\x1b]0;my title" + bel + "x", false},
		{"OSC 11 colour query", "\x1b]11;?" + bel + "x", "x", false},
		{"OSC 4 colour query", "\x1b]4;1;?" + st + "x", "x", false},
		{"OSC 11 colour set passes", "\x1b]11;#102030" + bel, "\x1b]11;#102030" + bel, false},
		{"CSI 21 t title report", "a\x1b[21tb", "ab", false},
		{"CSI 14 t size report", "\x1b[14t", "", false},
		{"CSI 22;0 t title push passes", "\x1b[22;0t", "\x1b[22;0t", false},
		{"CSI 8 t resize passes", "\x1b[8;40;120t", "\x1b[8;40;120t", false},
		{"DSR cursor", "\x1b[6n", "", false},
		{"DSR status", "\x1b[5n", "", false},
		{"DA1", "\x1b[c\x1b[0c", "", false},
		{"DA2", "\x1b[>c\x1b[>0c", "", false},
		{"kitty keyboard query", "\x1b[?u", "", false},
		{"kitty keyboard push passes", "\x1b[>1u", "\x1b[>1u", false},
		{"DECRQSS", "\x1b[0m\x1bP$qm" + st + "z", "\x1b[0mz", false},
		{"XTGETTCAP", "\x1bP+q544e" + st, "", false},
		{"a sixel DCS passes", "\x1bPq#0;2;0;0;0" + st, "\x1bPq#0;2;0;0;0" + st, false},
		{"bracketed paste mode passes", "\x1b[?2004h", "\x1b[?2004h", false},
		{"UTF-8 passes", "é→✓", "é→✓", false},
	}
	for _, c := range cases {
		// Whole, and cut at every byte: a sequence split across two reads is
		// read as one.
		for cut := 0; cut <= len(c.in); cut++ {
			f := outputFilter{osc52: c.osc52}
			got := string(f.filter([]byte(c.in[:cut]))) + string(f.filter([]byte(c.in[cut:])))
			if got != c.want {
				t.Errorf("%s (cut at %d): %q, want %q", c.name, cut, got, c.want)
				break
			}
		}
	}
}

// A string sequence (OSC, DCS) that never ends, past the bound, is dropped
// whole, never passed: an unbounded payload is no excuse to let it through.
func TestFilterOutputBound(t *testing.T) {
	f := outputFilter{}
	big := "\x1b]52;c;" + strings.Repeat("A", seqMax+10)
	got := string(f.filter([]byte(big))) + string(f.filter([]byte("\x07after")))
	if got != "after" {
		t.Fatalf("got %d bytes: %.40q", len(got), got)
	}
	f = outputFilter{}
	got = string(f.filter([]byte("\x1b]0;" + strings.Repeat("t", seqMax+10) + "\x07ok")))
	if got != "ok" {
		t.Fatalf("an overlong title: %.40q", got)
	}
}
