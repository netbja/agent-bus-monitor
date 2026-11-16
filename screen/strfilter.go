package screen

import (
	"bytes"
	"strconv"
)

// stringFilter drops, from the stream x/vt reads, the string sequences it
// would print (vt-osc-title, 2026-11-16). x/vt v0.1.0 honours 8-bit C1
// controls inside a string: a byte 0x80-0x9F in an OSC, DCS, APC, PM or SOS
// payload ends or breaks it there, and the rest of the payload lands on the
// screen. UTF-8 puts such bytes in many glyphs: Claude Code's title "✳
// <project>:<agent>" (E2 9C B3, 0x9C is ST) printed on its input line at each
// turn's end, its ready signature failed, and the dispatcher could no longer
// wake it.
//
// Dropped: every OSC 0, 1 and 2 (a title: nothing on the screen depends on
// it), and every string sequence whose payload holds a byte 0x80-0x9F. A
// sequence is held while it is read, across writes, and judged whole at its
// end (BEL for an OSC, as xterm, or ST); one longer than seqMax is dropped.
// A string cut by an ESC that does not make ST, or by CAN or SUB, ends there
// unwritten, as a terminal aborts it. Everything else passes byte for byte,
// in order.
type stringFilter struct {
	state int
	seq   []byte // ESC, the kind, the payload so far
	over  bool
	kind  byte // ']' OSC, 'P' DCS, '_' APC, '^' PM, 'X' SOS
}

// seqMax bounds one string sequence held while it is read.
const seqMax = 1 << 20

const (
	fGround = iota
	fEsc
	fStr
	fStrEsc // ESC inside a string: ST when '\' follows
)

// filter is what of p can reach the emulator now; a string not yet ended is
// held for the next call.
func (f *stringFilter) filter(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch f.state {
		case fGround:
			if b == 0x1b {
				f.state = fEsc
				continue
			}
			out = append(out, b)
		case fEsc:
			switch b {
			case ']', 'P', '_', '^', 'X':
				f.state, f.kind, f.over = fStr, b, false
				f.seq = append(f.seq[:0], 0x1b, b)
			case 0x1b: // ESC ESC: the first stands alone
				out = append(out, 0x1b)
			default:
				out = append(out, 0x1b, b)
				f.state = fGround
			}
		case fStr:
			switch {
			case b == 0x07 && f.kind == ']':
				out = f.end(out, []byte{0x07})
			case b == 0x1b:
				f.state = fStrEsc
			case b == 0x18 || b == 0x1a: // CAN, SUB: the string is aborted
				f.state, f.seq = fGround, f.seq[:0]
				out = append(out, b)
			default:
				f.hold(b)
			}
		case fStrEsc:
			if b == '\\' {
				out = f.end(out, []byte{0x1b, '\\'})
				continue
			}
			// ESC without '\': the string ends unwritten, the ESC starts anew.
			f.state, f.seq = fEsc, f.seq[:0]
			i-- // this byte follows that ESC
		}
	}
	return out
}

func (f *stringFilter) hold(b byte) {
	if len(f.seq) >= seqMax {
		f.over = true
		return
	}
	f.seq = append(f.seq, b)
}

// end judges the string just ended by term: kept (written with its
// terminator) or dropped.
func (f *stringFilter) end(out, term []byte) []byte {
	if !f.over && !f.drop() {
		out = append(append(out, f.seq...), term...)
	}
	f.state, f.seq = fGround, f.seq[:0]
	return out
}

// drop: a title, or a payload with a byte x/vt would take for a C1 control.
func (f *stringFilter) drop() bool {
	body := f.seq[2:]
	for _, b := range body {
		if b >= 0x80 && b <= 0x9f {
			return true
		}
	}
	if f.kind != ']' {
		return false
	}
	num, _, _ := bytes.Cut(body, []byte(";"))
	code, err := strconv.Atoi(string(num))
	return err == nil && code >= 0 && code <= 2
}
