package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A bubbletea key is the Key the tables match: the values tcell's adapter
// gave before step c, so every table answers as it did.
func TestKeyOfTea(t *testing.T) {
	for _, c := range []struct {
		tea  tea.Key
		want Key
	}{
		{tea.Key{Code: 'j', Text: "j"}, Key{Code: keyRune, Rune: 'j'}},
		{tea.Key{Code: 'r', Text: "R", Mod: tea.ModShift, ShiftedCode: 'R'}, Key{Code: keyRune, Rune: 'R'}},
		{tea.Key{Code: '!', Text: "!"}, Key{Code: keyRune, Rune: '!'}},
		{tea.Key{Code: 'x', Mod: tea.ModAlt}, Key{Code: keyRune, Rune: 'x', Alt: true}},
		{tea.Key{Code: tea.KeyEnter}, Key{Code: keyEnter}},
		{tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}, Key{Code: keyEnter, Alt: true}},
		{tea.Key{Code: tea.KeyEscape}, Key{Code: keyEscape}},
		{tea.Key{Code: tea.KeyTab}, Key{Code: keyTab}},
		{tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}, Key{Code: keyBacktab}},
		{tea.Key{Code: tea.KeyBackspace}, Key{Code: keyBackspace}},
		{tea.Key{Code: tea.KeyDelete}, Key{Code: keyDelete}},
		{tea.Key{Code: tea.KeyUp}, Key{Code: keyUp}},
		{tea.Key{Code: tea.KeyPgDown}, Key{Code: keyPgDn}},
		{tea.Key{Code: tea.KeyHome}, Key{Code: keyHome}},
		{tea.Key{Code: tea.KeyF5}, Key{Code: keyF5}},
		{tea.Key{Code: 'c', Mod: tea.ModCtrl}, Key{Code: keyCtrlC}},
		{tea.Key{Code: 'o', Mod: tea.ModCtrl}, Key{Code: keyCtrlO}},
		{tea.Key{Code: 's', Mod: tea.ModCtrl}, Key{Code: keyCtrlS}},
		{tea.Key{Code: 't', Mod: tea.ModCtrl}, Key{Code: keyCtrlT}},
		{tea.Key{Code: 'z', Mod: tea.ModCtrl}, Key{}},
		{tea.Key{Code: tea.KeyF12}, Key{}},
	} {
		if got := keyOfTea(tea.KeyPressMsg(c.tea)); got != c.want {
			t.Errorf("%v: %+v, want %+v", c.tea, got, c.want)
		}
	}
}
