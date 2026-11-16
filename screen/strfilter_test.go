package screen

import (
	"bytes"
	"strings"
	"testing"
)

// Each rule of stringFilter on its own, input bytes to output bytes, without
// x/vt: these keep the filter right when x/vt changes (review of 3ed67027).
func TestStringFilterRules(t *testing.T) {
	const star = "✳" // E2 9C B3: 0x9C is ST to x/vt
	for _, c := range []struct{ name, in, want string }{
		// The two drop rules, each alone.
		{"ASCII title, the title rule alone", "a\x1b]2;agent\x07b", "ab"},
		{"ASCII OSC 0 by ST", "a\x1b]0;agent\x1b\\b", "ab"},
		{"OSC 1", "a\x1b]1;x\x07b", "ab"},
		{"OSC 3 is no title", "a\x1b]3;x\x07b", "a\x1b]3;x\x07b"},
		{"OSC 10 is no title", "\x1b]10;?\x07", "\x1b]10;?\x07"},
		{"OSC 9 with a C1 byte, the C1 rule alone", "a\x1b]9;" + star + " waiting\x07b", "ab"},
		{"OSC 777 with a C1 byte", "a\x1b]777;notify;" + star + ";w\x1b\\b", "ab"},
		{"OSC 9 in ASCII passes", "\x1b]9;waiting\x07", "\x1b]9;waiting\x07"},
		{"a glyph without a C1 byte passes", "\x1b]9;·\x07", "\x1b]9;·\x07"},
		// Every string kind is held and judged, not only OSC.
		{"DCS with a C1 byte", "a\x1bP" + star + "\x1b\\b", "ab"},
		{"APC with a C1 byte", "a\x1b_G" + star + "\x1b\\b", "ab"},
		{"PM with a C1 byte", "a\x1b^" + star + "\x1b\\b", "ab"},
		{"SOS with a C1 byte", "a\x1bX" + star + "\x1b\\b", "ab"},
		{"DCS in ASCII passes", "\x1bPq#0\x1b\\", "\x1bPq#0\x1b\\"},
		{"BEL ends only an OSC", "x\x1bPa\x07" + star + "\x1b\\y", "xy"},
		// Aborts.
		{"CAN aborts a string", "a\x1b]0;t\x18b", "a\x18b"},
		{"SUB aborts a string", "a\x1b]9;t\x1ab", "a\x1ab"},
		{"ESC ESC: the first stands alone", "\x1b\x1b]0;t\x07b", "\x1bb"},
		{"ESC without '\\' ends the string, its byte kept", "a\x1b]0;t\x1b[1mb", "a\x1b[1mb"},
		{"ESC then a plain byte passes", "\x1b7x", "\x1b7x"},
		// The bound.
		{"payload at the bound passes", "\x1b]8;" + strings.Repeat("x", seqMax-4) + "\x07", "\x1b]8;" + strings.Repeat("x", seqMax-4) + "\x07"},
		{"payload past the bound is dropped", "a\x1b]8;" + strings.Repeat("x", seqMax-3) + "\x07b", "ab"},
	} {
		var f stringFilter
		if got := f.filter([]byte(c.in)); !bytes.Equal(got, []byte(c.want)) {
			g, w := string(got), c.want
			if len(g) > 40 || len(w) > 40 {
				t.Errorf("%s: got %d bytes, want %d", c.name, len(g), len(w))
				continue
			}
			t.Errorf("%s: got %q, want %q", c.name, g, w)
		}
	}
}

// A string is held across writes and judged at its end.
func TestStringFilterAcrossWrites(t *testing.T) {
	var f stringFilter
	var out []byte
	for _, p := range []string{"a\x1b", "]0;", "✳"[:2], "✳"[2:] + " t\x1b", "\\b\x1b]8;", "x\x07c"} {
		out = append(out, f.filter([]byte(p))...)
	}
	if want := "ab\x1b]8;x\x07c"; string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}
