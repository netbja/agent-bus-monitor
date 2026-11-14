package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/config"
)

// The attach prefix (herdr exit spec 4.6). The person's keys go to the
// program untouched except the prefix: <prefix> d detaches, <prefix>
// <prefix> sends one prefix, <prefix> r and <prefix> x release and discard
// keys the holder holds (only while it holds some); any other key after the
// prefix is dropped with a bell. The prefix is recognised in its legacy and
// kitty encodings (the outer terminal switches to kitty's whenever the
// program asks), split across reads; a kitty release or repeat of it is
// ignored.

type keyEventKind int

const (
	keySend    keyEventKind = iota // Data to the program
	keyPending                     // the prefix pressed: waiting for its second key
	keyDone                        // the wait ended with a literal prefix sent
	keyDetach
	keyRelease
	keyDiscard
	keyBell     // the key after the prefix is dropped
	keyFocusIn  // the outer terminal's focus report: the holder's, never the program's
	keyFocusOut //
)

type keyEvent struct {
	Kind keyEventKind
	Data []byte
}

func (e keyEvent) String() string {
	names := [...]string{"send", "pending", "done", "detach", "release", "discard", "bell", "focus-in", "focus-out"}
	if e.Kind == keySend {
		return fmt.Sprintf("send(%q)", e.Data)
	}
	return names[e.Kind]
}

// prefixKey is how the prefix arrives.
type prefixKey struct {
	c0       byte   // ctrl+<x>: its C0 byte (0 for a function key)
	kitty    int    // ctrl+<x>: its kitty code (CSI <code> ; 5 u)
	tilde    string // F5-F12, and F1-F4 in kitty's tilde form: CSI <n> ~
	ss3      byte   // F1-F4 legacy: ESC O <P-S>
	csiFinal byte   // F1-F4 kitty: CSI 1 ; <mods> <P-S>
}

func parsePrefix(name string) (prefixKey, error) {
	if !config.AttachPrefixAllowed(name) {
		return prefixKey{}, fmt.Errorf("%q is not an attach prefix (f1-f12, ctrl+<letter>, ctrl+\\, ctrl+]; not ctrl+c/d/h/i/j/m/q/s/z)", name)
	}
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok {
		r := rest[0]
		switch r {
		case '\\':
			return prefixKey{c0: 0x1c, kitty: '\\'}, nil
		case ']':
			return prefixKey{c0: 0x1d, kitty: ']'}, nil
		}
		return prefixKey{c0: r - 'a' + 1, kitty: int(r)}, nil
	}
	n, _ := strconv.Atoi(name[1:])
	tilde := map[int]string{1: "11", 2: "12", 3: "13", 4: "14", 5: "15", 6: "17", 7: "18", 8: "19", 9: "20", 10: "21", 11: "23", 12: "24"}[n]
	k := prefixKey{tilde: tilde}
	if n <= 4 {
		k.ss3, k.csiFinal = "PQRS"[n-1], "PQRS"[n-1]
	}
	return k, nil
}

// keyScanner turns the person's input into events. held is set by the
// attach loop from the holder's hold notices.
type keyScanner struct {
	key     prefixKey
	pending bool
	buf     []byte // an escape sequence not complete yet
	held    bool
}

func newKeyScanner(prefix string) (*keyScanner, error) {
	k, err := parsePrefix(prefix)
	if err != nil {
		return nil, err
	}
	return &keyScanner{key: k}, nil
}

// match says what unit is to the prefix: not it, its press, or a release or
// repeat of it (to drop).
type match int

const (
	notPrefix match = iota
	prefixPress
	prefixEcho // a kitty release or repeat of the prefix
)

// csiParams splits a CSI unit (ESC [ params final) into its parameters and
// final byte; ok false for anything else.
func csiParams(u []byte) (params []string, final byte, ok bool) {
	if len(u) < 3 || u[0] != 0x1b || u[1] != '[' {
		return nil, 0, false
	}
	return strings.Split(string(u[2:len(u)-1]), ";"), u[len(u)-1], true
}

// kittyEvent reads a "mods[:event]" parameter: plain is no modifier but the
// given one (1 none, 5 ctrl), and the event type (1 press, 2 repeat, 3
// release; absent is a press).
func kittyEvent(p string, wantMods string) (event int, ok bool) {
	mods, ev, _ := strings.Cut(p, ":")
	if mods == "" {
		mods = "1"
	}
	if mods != wantMods {
		return 0, false
	}
	switch ev {
	case "", "1":
		return 1, true
	case "2", "3":
		e, _ := strconv.Atoi(ev)
		return e, true
	}
	return 0, false
}

func (s *keyScanner) match(u []byte) match {
	k := s.key
	if k.c0 != 0 {
		if len(u) == 1 && u[0] == k.c0 {
			return prefixPress
		}
		if ps, final, ok := csiParams(u); ok && final == 'u' && len(ps) == 2 && ps[0] == strconv.Itoa(k.kitty) {
			if ev, ok := kittyEvent(ps[1], "5"); ok {
				return map[bool]match{true: prefixPress, false: prefixEcho}[ev == 1]
			}
		}
		return notPrefix
	}
	if k.ss3 != 0 && len(u) == 3 && u[0] == 0x1b && u[1] == 'O' && u[2] == k.ss3 {
		return prefixPress
	}
	ps, final, ok := csiParams(u)
	if !ok {
		return notPrefix
	}
	var ev int
	switch {
	case final == '~' && ps[0] == k.tilde && len(ps) == 1:
		return prefixPress
	case final == '~' && ps[0] == k.tilde && len(ps) == 2:
		if ev, ok = kittyEvent(ps[1], "1"); !ok {
			return notPrefix
		}
	case k.csiFinal != 0 && final == k.csiFinal && len(ps) == 2 && ps[0] == "1":
		if ev, ok = kittyEvent(ps[1], "1"); !ok {
			return notPrefix
		}
	case k.csiFinal != 0 && final == k.csiFinal && len(ps) == 1 && ps[0] == "":
		return prefixPress
	default:
		return notPrefix
	}
	if ev == 1 {
		return prefixPress
	}
	return prefixEcho
}

// plainKey is the letter a unit types, legacy or kitty (CSI <code> u, no
// modifier, a press); 0 for anything else.
func plainKey(u []byte) byte {
	if len(u) == 1 && u[0] >= 0x20 && u[0] < 0x7f {
		return u[0]
	}
	if ps, final, ok := csiParams(u); ok && final == 'u' && len(ps) <= 2 {
		code, err := strconv.Atoi(ps[0])
		if err != nil || code < 0x20 || code >= 0x7f {
			return 0
		}
		if len(ps) == 2 {
			if ev, ok := kittyEvent(ps[1], "1"); !ok || ev != 1 {
				return 0
			}
		}
		return byte(code)
	}
	return 0
}

// nextUnit splits b's first key unit: a CSI sequence (to its final byte), an
// SS3 one, ESC and one more byte, or one character. complete is false for an
// escape sequence that needs more bytes.
func nextUnit(b []byte) (unit []byte, complete bool) {
	if b[0] != 0x1b {
		_, n := utf8.DecodeRune(b)
		if b[0] < 0x80 {
			n = 1
		}
		return b[:n], true
	}
	if len(b) == 1 {
		return b, false
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				return b[:i+1], true
			}
		}
		return b, false
	case 'O':
		if len(b) < 3 {
			return b, false
		}
		return b[:3], true
	}
	return b[:2], true
}

// Feed takes one read of the person's input.
func (s *keyScanner) Feed(b []byte) []keyEvent {
	s.buf = append(s.buf, b...)
	var out []keyEvent
	var plain bytes.Buffer
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, keyEvent{Kind: keySend, Data: append([]byte(nil), plain.Bytes()...)})
			plain.Reset()
		}
	}
	for len(s.buf) > 0 {
		u, complete := nextUnit(s.buf)
		if !complete {
			break
		}
		s.buf = s.buf[len(u):]
		// The outer terminal's focus reports (attach asks for them) tell the
		// holder whether the person looks at this terminal; they are never
		// keys for the program, and a pending prefix keeps waiting.
		if string(u) == "\x1b[I" || string(u) == "\x1b[O" {
			flush()
			out = append(out, keyEvent{Kind: map[bool]keyEventKind{true: keyFocusIn, false: keyFocusOut}[u[2] == 'I']})
			continue
		}
		m := s.match(u)
		if !s.pending {
			switch m {
			case prefixPress:
				flush()
				s.pending = true
				out = append(out, keyEvent{Kind: keyPending})
			case prefixEcho: // a release or repeat of the prefix: never to the program
			default:
				plain.Write(u)
			}
			continue
		}
		switch k := plainKey(u); {
		case m == prefixEcho:
			continue // still waiting for the second key
		case m == prefixPress:
			out = append(out, keyEvent{Kind: keySend, Data: append([]byte(nil), u...)}, keyEvent{Kind: keyDone})
		case k == 'd':
			out = append(out, keyEvent{Kind: keyDetach})
		case k == 'r' && s.held:
			out = append(out, keyEvent{Kind: keyRelease})
		case k == 'x' && s.held:
			out = append(out, keyEvent{Kind: keyDiscard})
		default:
			out = append(out, keyEvent{Kind: keyBell})
		}
		s.pending = false
	}
	flush()
	return out
}

// Flush sends what waits in the buffer as it is: called when no more input
// came soon after a lone ESC (the Escape key itself) or a cut sequence.
func (s *keyScanner) Flush() []keyEvent {
	if len(s.buf) == 0 {
		return nil
	}
	b := s.buf
	s.buf = nil
	if s.pending { // the key after the prefix: dropped
		s.pending = false
		return []keyEvent{{Kind: keyBell}}
	}
	return []keyEvent{{Kind: keySend, Data: b}}
}
