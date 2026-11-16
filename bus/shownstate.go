package bus

// StateNoTerminal is the state shown for an agent whose holder terminal is
// gone (item 22): in place of its last declared state, never idle. It is a
// reader's projection: the agent's snapshot is never rewritten (putSnapshot).
const StateNoTerminal = "no terminal"

// ShownState is the state a reader shows for an agent: its declared state,
// or StateNoTerminal when it ran in a holder terminal (pane set) and the
// project's holder, read (termsRead), holds no live terminal of it (live).
// A holder not read (another host, or down) proves nothing: the declared
// state shows.
func ShownState(state, pane string, termsRead, live bool) string {
	if pane != "" && termsRead && !live {
		return StateNoTerminal
	}
	return state
}
