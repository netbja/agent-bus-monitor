package main

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// The bubbletea adapter of the key tables' Key (busmon Charm spec, step b).

var teaKeys = map[rune]KeyCode{
	tea.KeyEnter: keyEnter, tea.KeyEscape: keyEscape, tea.KeyTab: keyTab, tea.KeyBackspace: keyBackspace,
	tea.KeyDelete: keyDelete, tea.KeyUp: keyUp, tea.KeyDown: keyDown, tea.KeyLeft: keyLeft, tea.KeyRight: keyRight,
	tea.KeyHome: keyHome, tea.KeyEnd: keyEnd, tea.KeyPgUp: keyPgUp, tea.KeyPgDown: keyPgDn,
	tea.KeyF1: keyF1, tea.KeyF2: keyF2, tea.KeyF3: keyF3, tea.KeyF4: keyF4, tea.KeyF5: keyF5, tea.KeyF6: keyF6,
}

var teaCtrl = map[rune]KeyCode{'a': keyCtrlA, 'c': keyCtrlC, 'e': keyCtrlE, 'o': keyCtrlO, 's': keyCtrlS, 't': keyCtrlT, 'u': keyCtrlU, 'v': keyCtrlV, 'w': keyCtrlW, 'y': keyCtrlY, 'z': keyCtrlZ}

// keyOfTea is a bubbletea key press as a Key.
func keyOfTea(msg tea.KeyPressMsg) Key {
	k := msg.Key()
	alt := k.Mod.Contains(tea.ModAlt)
	if k.Mod.Contains(tea.ModCtrl) {
		// AltGr reported as Ctrl+Alt (kitty's ;7) is the character it made,
		// marked Alt like an Alt rune: a table matching that rune (runeIs
		// ignores Alt) reads it as it reads Alt+rune, so Ctrl+Alt+? opens the
		// help on the panels. A Ctrl+Alt letter stays a control key, and a
		// control code (Enter, Tab, Backspace) is no character (compose-dead-keys:
		// ~ on a Swiss French keyboard in a herdr pane).
		if alt && !unicode.IsLetter(k.Code) && unicode.IsPrint(k.Code) {
			return Key{Code: keyRune, Rune: k.Code, Alt: true}
		}
		if k.Code == 'z' && k.Mod.Contains(tea.ModShift) { // ctrl+shift+z redoes, where the terminal tells it apart
			return Key{Code: keyCtrlY, Alt: alt}
		}
		return Key{Code: teaCtrl[k.Code], Alt: alt}
	}
	if k.Code == tea.KeyTab && k.Mod.Contains(tea.ModShift) {
		return Key{Code: keyBacktab, Alt: alt}
	}
	if c, ok := teaKeys[k.Code]; ok {
		return Key{Code: c, Alt: alt, Shift: k.Mod.Contains(tea.ModShift)}
	}
	if r := []rune(k.Text); len(r) == 1 {
		return Key{Code: keyRune, Rune: r[0], Alt: alt}
	}
	if k.Text == "" && unicode.IsPrint(k.Code) { // alt+x carries no text
		r := k.Code
		if k.ShiftedCode != 0 {
			r = k.ShiftedCode
		} else if k.Mod.Contains(tea.ModShift) {
			r = unicode.ToUpper(r)
		}
		return Key{Code: keyRune, Rune: r, Alt: alt}
	}
	return Key{Alt: alt}
}
