package main

import (
	"github.com/gdamore/tcell/v2"
)

// Mode is who owns the keyboard. Capture order (spec §6): overlay, then the
// text modes, then the focused panel.
type Mode int

const (
	ModePanel Mode = iota
	ModeInput
	ModeFilter
	ModeOverlay
)

// Action is what a key means in a mode; the ui performs it.
type Action int

const (
	ActNone Action = iota
	ActQuit
	ActNextPanel
	ActPrevPanel
	ActTab1
	ActTab2
	ActTab3
	ActTab4
	ActUp
	ActDown
	ActPageUp
	ActPageDown
	ActFirst
	ActLast
	ActLive
	ActOpen
	ActCopy
	ActCopyThread
	ActFilter
	ActCompose
	ActComposeAt
	ActHelp
	ActRequests
	ActEscape
	ActSend
	ActClose
)

// binding is one row of a mode's key table. The same rows drive the handler
// (lookup), the footer (footerHints) and the KEYS section of the help, so the
// footer can never advertise a key that does something else.
type binding struct {
	Keys   string // as displayed: "↑↓ j k"
	Hint   string // footer word; "" = not advertised in the footer
	Act    Action
	Panels []PanelID // nil = every panel
	Tiers  []Tier    // nil = every tier
	match  func(*tcell.EventKey) bool
}

func (b binding) applies(p PanelID, t Tier) bool {
	return inPanels(b.Panels, p) && inTiers(b.Tiers, t)
}

func inPanels(ps []PanelID, p PanelID) bool {
	if ps == nil {
		return true
	}
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

func inTiers(ts []Tier, t Tier) bool {
	if ts == nil {
		return true
	}
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func keyIs(ks ...tcell.Key) func(*tcell.EventKey) bool {
	return func(ev *tcell.EventKey) bool {
		for _, k := range ks {
			if ev.Key() == k {
				return true
			}
		}
		return false
	}
}

func runeIs(rs ...rune) func(*tcell.EventKey) bool {
	return func(ev *tcell.EventKey) bool {
		if ev.Key() != tcell.KeyRune {
			return false
		}
		for _, r := range rs {
			if ev.Rune() == r {
				return true
			}
		}
		return false
	}
}

func either(fs ...func(*tcell.EventKey) bool) func(*tcell.EventKey) bool {
	return func(ev *tcell.EventKey) bool {
		for _, f := range fs {
			if f(ev) {
				return true
			}
		}
		return false
	}
}

var (
	quitBinding   = binding{Keys: "^C", Act: ActQuit, match: keyIs(tcell.KeyCtrlC)}
	notActivity   = []PanelID{PanelAgents, PanelBoard, PanelRequests}
	activityOnly  = []PanelID{PanelActivity}
	arrows        = GlyphUp + GlyphDownArrow
	panelBindings = []binding{
		{Keys: "tab", Hint: "focus", Act: ActNextPanel, match: keyIs(tcell.KeyTab)},
		{Keys: "shift-tab", Act: ActPrevPanel, match: keyIs(tcell.KeyBacktab)},
		{Keys: "1-3", Hint: "view", Act: ActTab1, Tiers: []Tier{TierMedium}, match: runeIs('1')},
		{Keys: "1-4", Hint: "view", Act: ActTab1, Tiers: []Tier{TierNarrow}, match: runeIs('1')},
		{Keys: "2", Act: ActTab2, Tiers: []Tier{TierMedium, TierNarrow}, match: runeIs('2')},
		{Keys: "3", Act: ActTab3, Tiers: []Tier{TierMedium, TierNarrow}, match: runeIs('3')},
		{Keys: "4", Act: ActTab4, Tiers: []Tier{TierNarrow}, match: runeIs('4')},
		{Keys: arrows + " j k", Hint: "select", Act: ActUp, match: either(keyIs(tcell.KeyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(tcell.KeyDown), runeIs('j'))},
		{Keys: "pgup", Act: ActPageUp, match: keyIs(tcell.KeyPgUp)},
		{Keys: "pgdn", Act: ActPageDown, match: keyIs(tcell.KeyPgDn)},
		{Keys: "home g", Act: ActFirst, match: either(keyIs(tcell.KeyHome), runeIs('g'))},
		{Keys: "end G", Hint: "live", Act: ActLive, Panels: activityOnly, match: either(keyIs(tcell.KeyEnd), runeIs('G'))},
		{Keys: "end G", Act: ActLast, Panels: notActivity, match: either(keyIs(tcell.KeyEnd), runeIs('G'))},
		{Keys: "enter", Hint: "detail", Act: ActOpen, match: either(keyIs(tcell.KeyEnter), runeIs('v'))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "/", Hint: "filter", Act: ActFilter, match: runeIs('/')},
		{Keys: "i", Hint: "write", Act: ActCompose, match: runeIs('i', ':')},
		{Keys: "@", Act: ActComposeAt, match: runeIs('@')},
		{Keys: "esc", Hint: "back", Act: ActEscape, match: keyIs(tcell.KeyEscape)},
		{Keys: "F2", Hint: "requests", Act: ActRequests, match: either(keyIs(tcell.KeyF2), runeIs('r'))},
		{Keys: "F1", Hint: "help", Act: ActHelp, match: either(keyIs(tcell.KeyF1), runeIs('?'))},
		{Keys: "q", Hint: "quit", Act: ActQuit, match: runeIs('q')},
	}
	inputBindings = []binding{
		{Keys: "enter", Hint: "send", Act: ActSend, match: keyIs(tcell.KeyEnter)},
		{Keys: "tab", Hint: "complete @agent", Act: ActNone, match: keyIs(tcell.KeyTab)},
		{Keys: "esc", Hint: "back (draft kept)", Act: ActEscape, match: keyIs(tcell.KeyEscape)},
	}
	filterBindings = []binding{
		{Keys: "enter", Hint: "apply", Act: ActSend, match: keyIs(tcell.KeyEnter)},
		{Keys: "esc", Hint: "cancel", Act: ActEscape, match: keyIs(tcell.KeyEscape)},
	}
	overlayBindings = []binding{
		{Keys: arrows + " j k", Hint: "scroll", Act: ActUp, match: either(keyIs(tcell.KeyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(tcell.KeyDown), runeIs('j'))},
		{Keys: "pgup", Act: ActPageUp, match: keyIs(tcell.KeyPgUp)},
		{Keys: "pgdn", Act: ActPageDown, match: keyIs(tcell.KeyPgDn)},
		{Keys: "home g", Act: ActFirst, match: either(keyIs(tcell.KeyHome), runeIs('g'))},
		{Keys: "end G", Act: ActLast, match: either(keyIs(tcell.KeyEnd), runeIs('G'))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "Y", Hint: "copy thread", Act: ActCopyThread, match: runeIs('Y')},
		{Keys: "esc q", Hint: "close", Act: ActClose, match: either(keyIs(tcell.KeyEscape), runeIs('q'))},
	}
)

// bindings is the key table of a mode, Ctrl-C (quit everywhere) last.
func bindings(m Mode) []binding {
	var bs []binding
	switch m {
	case ModePanel:
		bs = panelBindings
	case ModeInput:
		bs = inputBindings
	case ModeFilter:
		bs = filterBindings
	case ModeOverlay:
		bs = overlayBindings
	}
	return append(append([]binding{}, bs...), quitBinding)
}

// lookup returns the action bound to ev; ActNone when unbound — in the text
// modes that means the field gets the key (printable keys, digits, q, Tab).
func lookup(m Mode, p PanelID, t Tier, ev *tcell.EventKey) Action {
	for _, b := range bindings(m) {
		if b.applies(p, t) && b.match(ev) {
			return b.Act
		}
	}
	return ActNone
}

// footerHints renders the advertised bindings of the mode, in table order, as
// whole "keys hint" pairs; a pair that does not fit is left out, never cut.
func footerHints(m Mode, p PanelID, t Tier, width int) Line {
	l := Line{{Text: " "}}
	for _, b := range bindings(m) {
		if b.Hint == "" || !b.applies(p, t) {
			continue
		}
		pair := Line{{Text: b.Keys, Role: RoleAccent, Bold: true}, {Text: " " + b.Hint, Role: RoleMuted}}
		if len(l) > 1 {
			pair = append(Line{{Text: "   "}}, pair...)
		}
		if l.Width()+pair.Width() > width {
			continue
		}
		l = append(l, pair...)
	}
	return FitLine(l, width)
}
