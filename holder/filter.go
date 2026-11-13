package holder

import (
	"bytes"
	"strconv"
	"strings"
)

// seqMax bounds one escape sequence the filter holds while it is read; past
// it the sequence is dropped whole when it ends (an unbounded payload is no
// reason to let it through).
const seqMax = 1 << 20

// outputFilter is one viewer's output filter (spec 4.6, security S3). It
// reads the program's output as a stream (a sequence may be cut across two
// reads) and drops, before it reaches the person's terminal:
//   - OSC 52 (a clipboard write), unless attach.osc52 is on;
//   - OSC 8 hyperlinks (the link's text is kept: it is outside the sequence);
//   - the report queries whose replies the outer terminal would type back
//     into the program: window reports (CSI 11/13-16/18-21 t), DECRQSS and
//     XTGETTCAP (DCS $ q, DCS + q), OSC 4/10-19 colour queries ("?"), DA
//     (CSI c, CSI > c, CSI = c), DSR (CSI 5 n, CSI 6 n, CSI ? 6 n) and the
//     kitty keyboard query (CSI ? u). The holder's emulator answers the
//     program itself (terminal.readLoop).
//
// Everything else passes byte for byte.
type outputFilter struct {
	osc52 bool

	state   int
	seq     []byte // the sequence being read, ESC first
	over    bool   // seq passed seqMax: dropped at its end
	strKind byte   // ']' OSC, 'P' DCS, '_' APC, '^' PM, 'X' SOS
}

const (
	stGround = iota
	stEsc
	stCSI
	stStr
	stStrEsc // ESC inside a string: ST if '\' follows
)

func (f *outputFilter) hold(b byte) {
	if len(f.seq) >= seqMax {
		f.over = true
		return
	}
	f.seq = append(f.seq, b)
}

// filter is the filtered part of p that can be written now; a sequence not
// yet ended is kept for the next call.
func (f *outputFilter) filter(p []byte) []byte {
	var out []byte
	for _, b := range p {
		switch f.state {
		case stGround:
			if b == 0x1b {
				f.state, f.seq, f.over = stEsc, append(f.seq[:0], b), false
				continue
			}
			out = append(out, b)
		case stEsc:
			f.seq = append(f.seq, b)
			switch b {
			case '[':
				f.state = stCSI
			case ']', 'P', '_', '^', 'X':
				f.state, f.strKind = stStr, b
			case 0x1b:
				out = append(out, 0x1b) // ESC ESC: the first stands alone
				f.seq = f.seq[1:]
			default:
				out = append(out, f.seq...)
				f.state = stGround
			}
		case stCSI:
			f.hold(b)
			if b >= 0x40 && b <= 0x7e {
				if !f.over && !dropCSI(f.seq) {
					out = append(out, f.seq...)
				}
				f.state = stGround
			}
		case stStr:
			switch {
			case b == 0x07 && f.strKind == ']': // xterm ends an OSC with BEL too
				f.hold(b)
				out = f.endString(out)
			case b == 0x1b:
				f.state = stStrEsc
			default:
				f.hold(b)
			}
		case stStrEsc:
			if b == '\\' {
				f.hold(0x1b)
				f.hold(b)
				out = f.endString(out)
				continue
			}
			// ESC without '\': the string ends unterminated (dropped), and
			// this ESC starts anew.
			f.state, f.seq, f.over = stEsc, append(f.seq[:0], 0x1b), false
			out = f.filter2(out, b)
		}
	}
	return out
}

// filter2 feeds one byte back through the machine (after an unterminated
// string).
func (f *outputFilter) filter2(out []byte, b byte) []byte {
	return append(out, f.filter([]byte{b})...)
}

func (f *outputFilter) endString(out []byte) []byte {
	if !f.over && !f.dropString() {
		out = append(out, f.seq...)
	}
	f.state = stGround
	return out
}

// dropString decides an ended OSC or DCS (f.seq: ESC, the kind, the payload,
// the terminator).
func (f *outputFilter) dropString() bool {
	body := f.seq[2:]
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte{0x1b, '\\'}), []byte{0x07})
	switch f.strKind {
	case 'P':
		return bytes.HasPrefix(body, []byte("$q")) || bytes.HasPrefix(body, []byte("+q"))
	case ']':
		num, rest, _ := bytes.Cut(body, []byte(";"))
		code, err := strconv.Atoi(string(num))
		if err != nil {
			return false
		}
		switch {
		case code == 52:
			return !f.osc52
		case code == 8:
			return true
		case code == 4 || code >= 10 && code <= 19:
			for _, field := range bytes.Split(rest, []byte(";")) {
				if bytes.Equal(field, []byte("?")) {
					return true
				}
			}
		}
	}
	return false
}

// reportCodes are the window operations (CSI Ps t) that make the terminal
// answer.
var reportCodes = map[string]bool{"11": true, "13": true, "14": true, "15": true, "16": true, "18": true, "19": true, "20": true, "21": true}

// dropCSI decides an ended CSI (seq: ESC [ params… final).
func dropCSI(seq []byte) bool {
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	switch final {
	case 't':
		first, _, _ := strings.Cut(params, ";")
		return reportCodes[first]
	case 'n':
		return params == "5" || params == "6" || params == "?6"
	case 'c':
		switch params {
		case "", "0", ">", ">0", "=", "=0":
			return true
		}
	case 'u':
		return params == "?"
	}
	return false
}
