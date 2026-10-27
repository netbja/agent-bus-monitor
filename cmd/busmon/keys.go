package main

// Mode is who owns the keyboard. Capture order (spec §6): overlay, then the
// text modes, then the focused panel.
type Mode int

const (
	ModePanel Mode = iota
	ModeInput
	ModeFilter
	ModeOverlay
	ModeAsk      // the ask panel: answering an agent's questions
	ModeCompose  // the compose window (v0.16): a message or a free answer, several lines
	ModePick     // busmon --pick (v0.20): the Projects picker, its own screen
	ModeShell    // the step-b shell's panels (busmon Charm spec): ModePanel's table with the shell's tabs
	ModeShellAsk // the shell's reader on an ask: ModeAsk's table, where a digit answers
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
	ActServices
	ActEscape
	ActSend
	ActClose
	ActDelete
	ActShowAll
	ActConfirm
	ActFollow
	ActNextUnread
	ActAsk
	ActAskNext
	ActAskPrev
	ActAskPick
	ActComposeOpen  // open the compose window
	ActComposeField // the window: text <-> recipient
	ActComposeAsk   // the window: answer the open question in its panel
	ActRecap        // open the deterministic project recap
	ActHealth       // open project health details
	ActPickType     // the picker: a typed character or backspace edits the filter
	ActPlan         // open the batch's plan
	ActTab5         // the shell's narrow tier: the fifth view
	ActAskOther     // the shell's ask: a free answer ("Other") in the compose window
)

// binding is one row of a mode's key table. The same rows drive the handler
// (lookup), the footer (footerHints) and the KEYS section of the help, so the
// footer can never advertise a key that does something else.
type binding struct {
	Keys   string // as displayed: "↑↓ j k"
	Hint   string // footer word; "" = not advertised in the footer
	Help   string // help description when Hint is empty; "" = folded into another row
	Alt    string // alternate keys shown in the help only: "v" for enter
	Act    Action
	Panels []PanelID // nil = every panel
	Tiers  []Tier    // nil = every tier
	match  func(Key) bool
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

func keyIs(ks ...KeyCode) func(Key) bool {
	return func(ev Key) bool {
		for _, k := range ks {
			if ev.Code == k {
				return true
			}
		}
		return false
	}
}

func runeIs(rs ...rune) func(Key) bool {
	return func(ev Key) bool {
		if ev.Code != keyRune {
			return false
		}
		for _, r := range rs {
			if ev.Rune == r {
				return true
			}
		}
		return false
	}
}

func either(fs ...func(Key) bool) func(Key) bool {
	return func(ev Key) bool {
		for _, f := range fs {
			if f(ev) {
				return true
			}
		}
		return false
	}
}

var (
	quitBinding   = binding{Keys: "^C", Help: "quit (everywhere)", Act: ActQuit, match: keyIs(keyCtrlC)}
	notActivity   = []PanelID{PanelAgents, PanelBoard, PanelRequests}
	activityOnly  = []PanelID{PanelActivity}
	arrows        = GlyphUp + GlyphDownArrow
	panelBindings = []binding{
		{Keys: "tab", Hint: "focus", Act: ActNextPanel, match: keyIs(keyTab)},
		{Keys: "shift-tab", Help: "focus the previous panel", Act: ActPrevPanel, match: keyIs(keyBacktab)},
		{Keys: "F5", Hint: "health", Act: ActHealth, match: keyIs(keyF5)},
		{Keys: "1-3", Hint: "view", Act: ActTab1, Tiers: []Tier{TierMedium}, match: runeIs('1')},
		{Keys: "1-4", Hint: "view", Act: ActTab1, Tiers: []Tier{TierNarrow}, match: runeIs('1')},
		{Keys: "2", Act: ActTab2, Tiers: []Tier{TierMedium, TierNarrow}, match: runeIs('2')},
		{Keys: "3", Act: ActTab3, Tiers: []Tier{TierMedium, TierNarrow}, match: runeIs('3')},
		{Keys: "4", Act: ActTab4, Tiers: []Tier{TierNarrow}, match: runeIs('4')},
		{Keys: arrows + " j k", Hint: "select", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: "pgup", Help: "page up", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Help: "page down", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "home g", Help: "first row", Act: ActFirst, match: either(keyIs(keyHome), runeIs('g'))},
		{Keys: "end G", Hint: "live", Act: ActLive, Panels: activityOnly, match: either(keyIs(keyEnd), runeIs('G'))},
		{Keys: "end G", Help: "last row", Act: ActLast, Panels: notActivity, match: either(keyIs(keyEnd), runeIs('G'))},
		{Keys: "enter", Hint: "detail", Alt: "v", Act: ActOpen, match: either(keyIs(keyEnter), runeIs('v'))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "f", Hint: "auto-scroll", Act: ActFollow, match: runeIs('f')},
		{Keys: "u", Help: "Activity: the next unread message for you (" + GlyphDotFull + "), wrapping", Act: ActNextUnread, match: runeIs('u')},
		{Keys: "! w", Hint: "answer", Alt: "F4", Act: ActAsk, match: either(keyIs(keyF4), runeIs('!', 'w'))},
		{Keys: "d del", Hint: "remove", Act: ActDelete, Panels: []PanelID{PanelAgents, PanelRequests}, match: either(runeIs('d'), keyIs(keyDelete))},
		{Keys: "a", Help: "show or hide the agents silent for more than a day", Act: ActShowAll, Panels: []PanelID{PanelAgents}, match: runeIs('a')},
		{Keys: "/", Hint: "filter", Act: ActFilter, match: runeIs('/')},
		{Keys: "i", Hint: "write", Alt: ":", Act: ActCompose, match: runeIs('i', ':')},
		{Keys: "@", Act: ActComposeAt, match: runeIs('@')},
		{Keys: "I", Help: "write in the compose window (several lines, Ctrl+S sends)", Act: ActComposeOpen, match: runeIs('I')},
		{Keys: "esc", Hint: "back", Act: ActEscape, match: keyIs(keyEscape)},
		{Keys: "F2", Hint: "requests", Alt: "r", Act: ActRequests, match: either(keyIs(keyF2), runeIs('r'))},
		{Keys: "F3", Hint: "services", Alt: "s", Act: ActServices, match: either(keyIs(keyF3), runeIs('s'))},
		{Keys: "R", Help: "where was I? open the project recap", Act: ActRecap, match: runeIs('R')},
		{Keys: "P", Help: "the batch's plan: its tasks by state (enter opens one, or expands done)", Act: ActPlan, match: runeIs('P')},
		{Keys: "F1", Hint: "help", Alt: "?", Act: ActHelp, match: either(keyIs(keyF1), runeIs('?'))},
		{Keys: "q", Hint: "quit", Act: ActQuit, match: runeIs('q')},
	}
	// shellTabs: in the shell, 1-3 pick the tabbed panel's tab at every
	// width; the narrow tier's five views take 1-5 (For you first).
	shellTabs = []binding{
		{Keys: "1-3", Hint: "tabs", Act: ActTab1, Tiers: []Tier{TierWide, TierMedium}, match: runeIs('1')},
		{Keys: "1-5", Hint: "view", Act: ActTab1, Tiers: []Tier{TierNarrow}, match: runeIs('1')},
		{Keys: "2", Act: ActTab2, match: runeIs('2')},
		{Keys: "3", Act: ActTab3, match: runeIs('3')},
		{Keys: "4", Act: ActTab4, Tiers: []Tier{TierNarrow}, match: runeIs('4')},
		{Keys: "5", Act: ActTab5, Tiers: []Tier{TierNarrow}, match: runeIs('5')},
	}
	shellBindings = shellPanelBindings()
	// shellAskBindings: in the shell's reader a digit answers with that option,
	// as a click on it does (busmon Charm spec, "For you"), and o is "Other".
	shellAskBindings = shellAskTable()
	inputBindings    = []binding{
		{Keys: "^O", Hint: "several lines", Alt: "alt-enter", Act: ActComposeOpen, match: either(keyIs(keyCtrlO), altEnter)},
		{Keys: "enter", Hint: "send", Act: ActSend, match: keyIs(keyEnter)},
		{Keys: "tab", Hint: "complete @agent", Act: ActNone, match: keyIs(keyTab)},
		{Keys: "esc", Hint: "back (draft kept)", Act: ActEscape, match: keyIs(keyEscape)},
	}
	filterBindings = []binding{
		{Keys: "enter", Hint: "apply", Act: ActSend, match: keyIs(keyEnter)},
		{Keys: "esc", Hint: "cancel", Act: ActEscape, match: keyIs(keyEscape)},
	}
	askBindings = []binding{
		{Keys: arrows + " j k", Hint: "choose", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: "1-9", Help: "choose an option by its number", Act: ActAskPick, match: runeIs('1', '2', '3', '4', '5', '6', '7', '8', '9')},
		{Keys: "enter", Hint: "answer", Act: ActSend, match: keyIs(keyEnter)},
		{Keys: GlyphArrowL + GlyphArrowR + " tab", Hint: "question", Act: ActAskNext, match: either(keyIs(keyRight), keyIs(keyTab))},
		{Keys: GlyphArrowL, Act: ActAskPrev, match: either(keyIs(keyLeft), keyIs(keyBacktab))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "?", Help: "help", Act: ActHelp, match: runeIs('?')},
		{Keys: "esc", Hint: "close", Act: ActClose, match: keyIs(keyEscape)},
	}
	composeBindings = []binding{
		{Keys: "^S", Hint: "send", Act: ActSend, match: keyIs(keyCtrlS)},
		{Keys: "tab", Hint: "recipient", Help: "recipient " + GlyphNone + " text; a text that is only @prefix becomes the recipient (completed, or its list opens)", Act: ActComposeField, match: keyIs(keyTab)},
		{Keys: "^T", Help: "after the open-question hint: answer in its question panel", Act: ActComposeAsk, match: keyIs(keyCtrlT)},
		{Keys: "esc", Hint: "close (draft kept)", Act: ActClose, match: keyIs(keyEscape)},
	}
	pickBindings = []binding{
		{Keys: "type", Hint: "to filter", Act: ActPickType, match: keyIs(keyRune, keyBackspace, keyBackspace)},
		{Keys: arrows, Hint: "select", Act: ActUp, match: keyIs(keyUp)},
		{Keys: GlyphDownArrow, Act: ActDown, match: keyIs(keyDown)},
		{Keys: "pgup", Help: "page up", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Help: "page down", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "enter", Hint: "open", Act: ActOpen, match: keyIs(keyEnter)},
		{Keys: "esc", Hint: "close", Act: ActClose, match: keyIs(keyEscape)},
	}
	overlayBindings = []binding{
		{Keys: "F5", Hint: "health", Act: ActHealth, match: keyIs(keyF5)},
		{Keys: "i I", Help: "in an agent's detail: reply to that agent in the compose window", Act: ActComposeOpen, match: runeIs('i', 'I')},
		{Keys: arrows + " j k", Hint: "scroll", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: "pgup", Help: "page up", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Help: "page down", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "home g", Help: "first row", Act: ActFirst, match: either(keyIs(keyHome), runeIs('g'))},
		{Keys: "end G", Help: "last row", Act: ActLast, match: either(keyIs(keyEnd), runeIs('G'))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "Y", Hint: "copy thread", Act: ActCopyThread, match: runeIs('Y')},
		{Keys: "enter ! w", Help: "answer the agent's open questions", Act: ActAsk, match: either(keyIs(keyEnter), keyIs(keyF4), runeIs('!', 'w'))},
		{Keys: "esc q", Hint: "close", Act: ActClose, match: either(keyIs(keyEscape), runeIs('q'))},
	}
)

// shellPanelBindings is the panel table for the shell: the same rows, in the
// same order, with its tabs in place of tview's, and the last-row key in For
// you as in the other lists.
func shellPanelBindings() []binding {
	var out []binding
	for _, b := range panelBindings {
		switch b.Act {
		case ActTab1, ActTab2, ActTab3, ActTab4:
			if b.Keys == "1-3" {
				out = append(out, shellTabs...)
			}
			continue
		case ActLast:
			b.Panels = append(append([]PanelID{}, b.Panels...), PanelForYou)
		}
		out = append(out, b)
	}
	return out
}

// shellAskTable is askBindings with the digits answering and o for "Other".
func shellAskTable() []binding {
	var out []binding
	for _, b := range askBindings {
		if b.Act == ActAskPick {
			b.Help = "answer with the option of that number"
			out = append(out, b, binding{Keys: "o", Help: "Other: a free answer in the compose window", Act: ActAskOther, match: runeIs('o')})
			continue
		}
		out = append(out, b)
	}
	return out
}

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
	case ModeAsk:
		bs = askBindings
	case ModeCompose:
		bs = composeBindings
	case ModePick:
		bs = pickBindings
	case ModeShell:
		bs = shellBindings
	case ModeShellAsk:
		bs = shellAskBindings
	}
	return append(append([]binding{}, bs...), quitBinding)
}

// lookup returns the action bound to ev; ActNone when unbound — in the text
// modes that means the field gets the key (printable keys, digits, q, Tab).
func lookup(m Mode, p PanelID, t Tier, ev Key) Action {
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

// altEnter is Alt+Enter (most terminals send it as Esc then Enter, which
// tcell reports as Enter with the Alt modifier).
func altEnter(k Key) bool { return k.Code == keyEnter && k.Alt }
