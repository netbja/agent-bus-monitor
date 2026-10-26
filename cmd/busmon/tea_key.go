package main

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// The bubbletea adapter of the key tables' Key (busmon Charm spec, step b):
// the same tables match it as they match keyOf's tcell keys.

var teaKeys = map[rune]KeyCode{
	tea.KeyEnter: keyEnter, tea.KeyEscape: keyEscape, tea.KeyTab: keyTab, tea.KeyBackspace: keyBackspace,
	tea.KeyDelete: keyDelete, tea.KeyUp: keyUp, tea.KeyDown: keyDown, tea.KeyLeft: keyLeft, tea.KeyRight: keyRight,
	tea.KeyHome: keyHome, tea.KeyEnd: keyEnd, tea.KeyPgUp: keyPgUp, tea.KeyPgDown: keyPgDn,
	tea.KeyF1: keyF1, tea.KeyF2: keyF2, tea.KeyF3: keyF3, tea.KeyF4: keyF4, tea.KeyF5: keyF5,
}

var teaCtrl = map[rune]KeyCode{'c': keyCtrlC, 'o': keyCtrlO, 's': keyCtrlS, 't': keyCtrlT}

// keyOfTea is a bubbletea key press as a Key.
func keyOfTea(msg tea.KeyPressMsg) Key {
	k := msg.Key()
	alt := k.Mod.Contains(tea.ModAlt)
	if k.Mod.Contains(tea.ModCtrl) {
		return Key{Code: teaCtrl[k.Code], Alt: alt}
	}
	if k.Code == tea.KeyTab && k.Mod.Contains(tea.ModShift) {
		return Key{Code: keyBacktab, Alt: alt}
	}
	if c, ok := teaKeys[k.Code]; ok {
		return Key{Code: c, Alt: alt}
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
