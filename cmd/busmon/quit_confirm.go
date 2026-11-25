package main

import tea "charm.land/bubbletea/v2"

// q asks before busmon quits (busmon-quit-confirm, the person): the confirm
// dialog, [ Quit ] [ Stay ], Stay focused (busmon-confirm-dialog). Ctrl-C
// quits at once (key), and display.confirm_quit off makes q quit at once too.

// askQuit is q: the dialog, or the quit when the setting is off.
func (m *Model) askQuit() tea.Cmd {
	if !m.cfg.settings.ConfirmQuit {
		return tea.Quit
	}
	m.openConfirm(&confirmDialog{Title: "Quit busmon?", Yes: "Quit", No: "Stay",
		OnYes: func(*Model) tea.Cmd { return tea.Quit }})
	return nil
}
