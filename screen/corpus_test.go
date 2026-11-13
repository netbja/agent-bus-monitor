package screen

import "strings"

// corpusCase is a raw byte stream and the screen it is played on. The
// expected screen of each is gostty's (Ghostty's VT core), committed in
// testdata/oracle/<name>.txt by `go test -tags gostty -run TestWriteOracle
// -update-oracle` (oracle_gostty_test.go); no case scrolls past its rows,
// so the oracle's text is the visible screen.
type corpusCase struct {
	name       string
	cols, rows int
	data       string
}

const esc = "\x1b"

// corpus covers what the clients do (spec 6.4, 9: Ink and ratatui redraws,
// wide chars, synchronized output) beyond what the captured screens show.
var corpus = []corpusCase{
	{"plain-lines", 40, 6, "one\r\ntwo\r\nthree"},
	{"cr-spinner", 40, 4, "⠋ working\r⠙ working\r⠹ working\r⠸ done   "},
	{"ink-erase-lines", 40, 8, // Ink's log-update: erase the last frame line by line, then draw the next
		"frame 1 line a\r\nframe 1 line b\r\nframe 1 line c" +
			esc + "[2K" + esc + "[1A" + esc + "[2K" + esc + "[1A" + esc + "[2K" + esc + "[G" +
			"frame 2 line a\r\nframe 2 b"},
	{"wide-glyphs", 20, 6, "❯ ask\r\n› codex\r\n🌒 · Tip\r\n日本語テキスト\r\n─────"},
	{"wide-at-right-edge", 10, 4, "123456789日x"}, // the wide char does not fit in column 10: it wraps
	{"autowrap-and-phantom", 10, 4, "0123456789" + "abc" + "\r\n" + "0123456789" + esc + "[1;1HZ"},
	{"dim-placeholder", 40, 4, "─────\r\n❯ " + esc + "[2mTry \"how do I log\"" + esc + "[0m"},
	{"sgr-mix", 40, 3, esc + "[1;31mbold red" + esc + "[0m " + esc + "[38;5;208morange" + esc + "[0m " + esc + "[38;2;1;2;3mrgb" + esc + "[m end"},
	{"alt-screen-roundtrip", 30, 5, "main text\r\n" + esc + "[?1049h" + esc + "[Hfull screen app" + esc + "[?1049l" + "back"},
	{"sync-output", 30, 4, esc + "[?2026h" + "frame" + esc + "[2K\rfinal frame" + esc + "[?2026l"},
	{"scroll-region", 30, 6, "l1\r\nl2\r\nl3\r\nl4\r\nl5\r\nl6" + esc + "[2;4r" + esc + "[4;1H\nNEW" + esc + "[r"},
	{"insert-delete", 30, 5, "aaa\r\nbbb\r\nccc\r\nddd" + esc + "[2;1H" + esc + "[L" + "ins" + esc + "[4;1H" + esc + "[M" + esc + "[1;1H" + esc + "[2@" + esc + "[1;5H" + esc + "[1P"},
	{"erase-display", 30, 4, "keep\r\nwipe this\r\nand this" + esc + "[2;3H" + esc + "[J"},
	{"save-restore-cursor", 30, 3, "start" + esc + "7" + esc + "[3;1Hbottom" + esc + "8" + " mid"},
	{"reverse-index", 30, 4, "top\r\nnext" + esc + "[1;1H" + esc + "M" + "new top"},
	{"tabs", 40, 3, "a\tb\tc\r\n\tindented"},
	{"combining", 20, 3, "été\r\nñ"},
	{"cursor-moves", 30, 5, esc + "[3;10Hmid" + esc + "[1;1Htop" + esc + "[5;1Hbottom" + esc + "[2;5H" + esc + "[3Cx" + esc + "[2Dy"},
	{"box-drawing", 20, 4, "╭──────╮\r\n│ box  │\r\n╰──────╯"},
	{"osc-title-and-link", 40, 3, esc + "]0;a title\a" + "text " + esc + "]8;;https://x.test\a" + "link" + esc + "]8;;\a" + " after"},
}

// replayable turns a captured screen (lines joined by \n, some ending in \r)
// into the bytes a program would have written: \r\n between lines.
func replayable(screen string) string {
	return strings.ReplaceAll(strings.ReplaceAll(screen, "\r\n", "\n"), "\n", "\r\n")
}
