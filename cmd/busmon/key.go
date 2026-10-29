package main

// Key is a key press as the key tables match it, independent of the terminal
// library (busmon Charm spec, step a): keyOfTea adapts bubbletea's keys. The
// tables stay the one source of the handler, the footer and the help.
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
	keyCtrlA
	keyCtrlC
	keyCtrlE
	keyCtrlO
	keyCtrlS
	keyCtrlT
	keyCtrlU
	keyCtrlW
)
