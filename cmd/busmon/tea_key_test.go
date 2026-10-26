package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gdamore/tcell/v2"
)

// A bubbletea key and its tcell twin are the same Key, so the key tables
// answer both alike.
func TestKeyOfTeaMatchesKeyOf(t *testing.T) {
	for _, c := range []struct {
		tea  tea.Key
		tcel *tcell.EventKey
	}{
		{tea.Key{Code: 'j', Text: "j"}, tcell.NewEventKey(tcell.KeyRune, 'j', 0)},
		{tea.Key{Code: 'r', Text: "R", Mod: tea.ModShift, ShiftedCode: 'R'}, tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModShift)},
		{tea.Key{Code: '!', Text: "!"}, tcell.NewEventKey(tcell.KeyRune, '!', 0)},
		{tea.Key{Code: 'x', Mod: tea.ModAlt}, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt)},
		{tea.Key{Code: tea.KeyEnter}, tcell.NewEventKey(tcell.KeyEnter, 0, 0)},
		{tea.Key{Code: tea.KeyEscape}, tcell.NewEventKey(tcell.KeyEscape, 0, 0)},
		{tea.Key{Code: tea.KeyTab}, tcell.NewEventKey(tcell.KeyTab, 0, 0)},
		{tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}, tcell.NewEventKey(tcell.KeyBacktab, 0, 0)},
		{tea.Key{Code: tea.KeyBackspace}, tcell.NewEventKey(tcell.KeyBackspace2, 0, 0)},
		{tea.Key{Code: tea.KeyDelete}, tcell.NewEventKey(tcell.KeyDelete, 0, 0)},
		{tea.Key{Code: tea.KeyUp}, tcell.NewEventKey(tcell.KeyUp, 0, 0)},
		{tea.Key{Code: tea.KeyPgDown}, tcell.NewEventKey(tcell.KeyPgDn, 0, 0)},
		{tea.Key{Code: tea.KeyHome}, tcell.NewEventKey(tcell.KeyHome, 0, 0)},
		{tea.Key{Code: tea.KeyF5}, tcell.NewEventKey(tcell.KeyF5, 0, 0)},
		{tea.Key{Code: 'c', Mod: tea.ModCtrl}, tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)},
		{tea.Key{Code: 'o', Mod: tea.ModCtrl}, tcell.NewEventKey(tcell.KeyCtrlO, 0, tcell.ModCtrl)},
		{tea.Key{Code: 'z', Mod: tea.ModCtrl}, tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)},
		{tea.Key{Code: tea.KeyF12}, tcell.NewEventKey(tcell.KeyF12, 0, 0)},
	} {
		if got, want := keyOfTea(tea.KeyPressMsg(c.tea)), keyOf(c.tcel); got != want {
			t.Errorf("%v: tea %+v, tcell %+v", c.tea, got, want)
		}
	}
}
