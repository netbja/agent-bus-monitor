package screen

import "github.com/charmbracelet/x/ansi"

// modeTracker follows the private modes the emulator does not expose:
// bracketed paste (?2004) and synchronized output (?2026), set by DECSET
// (CSI ? n h) and reset by DECRST (CSI ? n l), in any chunking. It parses
// the same stream as the emulator; only those two modes are kept.
type modeTracker struct {
	p     *ansi.Parser
	modes Modes
}

func (t *modeTracker) feed(b []byte) {
	if t.p == nil {
		t.p = ansi.NewParser()
		t.p.SetHandler(ansi.Handler{HandleCsi: t.csi})
	}
	t.p.Parse(b)
}

func (t *modeTracker) csi(cmd ansi.Cmd, params ansi.Params) {
	if cmd.Prefix() != '?' || cmd.Intermediate() != 0 || (cmd.Final() != 'h' && cmd.Final() != 'l') {
		return
	}
	on := cmd.Final() == 'h'
	for _, p := range params {
		switch p.Param(-1) {
		case 2004:
			t.modes.BracketedPaste = on
		case 2026:
			t.modes.SyncOutput = on
		}
	}
}
