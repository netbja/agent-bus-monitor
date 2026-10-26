package main

import "github.com/gdamore/tcell/v2"

// Key is a key press as the key tables match it, independent of the terminal
// library (busmon Charm spec, step a): keyOf is the tcell adapter; the
// bubbletea one comes with the Elm shell. The tables stay the one source of
// the handler, the footer and the help.
type Key struct {
	Code KeyCode
	Rune rune // the character, for keyRune
	Alt  bool
}

// KeyCode names a key a table binds; keyNone is every other key.
type KeyCode int

const (
	keyNone KeyCode = iota
	keyRune
	keyEnter
	keyEscape
	keyTab
	keyBacktab
	keyBackspace
	keyDelete
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPgUp
	keyPgDn
	keyF1
	keyF2
	keyF3
	keyF4
	keyF5
	keyCtrlC
	keyCtrlO
	keyCtrlS
	keyCtrlT
)

var tcellKeys = map[tcell.Key]KeyCode{
	tcell.KeyRune: keyRune, tcell.KeyEnter: keyEnter, tcell.KeyEscape: keyEscape, tcell.KeyTab: keyTab,
	tcell.KeyBacktab: keyBacktab, tcell.KeyBackspace: keyBackspace, tcell.KeyBackspace2: keyBackspace,
	tcell.KeyDelete: keyDelete, tcell.KeyUp: keyUp, tcell.KeyDown: keyDown, tcell.KeyLeft: keyLeft,
	tcell.KeyRight: keyRight, tcell.KeyHome: keyHome, tcell.KeyEnd: keyEnd, tcell.KeyPgUp: keyPgUp,
	tcell.KeyPgDn: keyPgDn, tcell.KeyF1: keyF1, tcell.KeyF2: keyF2, tcell.KeyF3: keyF3, tcell.KeyF4: keyF4,
	tcell.KeyF5: keyF5, tcell.KeyCtrlC: keyCtrlC, tcell.KeyCtrlO: keyCtrlO, tcell.KeyCtrlS: keyCtrlS,
	tcell.KeyCtrlT: keyCtrlT,
}

// keyOf is a tcell key event as a Key.
func keyOf(ev *tcell.EventKey) Key {
	k := Key{Code: tcellKeys[ev.Key()], Alt: ev.Modifiers()&tcell.ModAlt != 0}
	if k.Code == keyRune {
		k.Rune = ev.Rune()
	}
	return k
}
