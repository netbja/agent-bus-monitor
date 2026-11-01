package main

import (
	"sort"
	"strconv"
	"strings"
)

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
	ModeTour     // the first-launch tour (tour.go): its pages own the keyboard
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
	ActTab6         // the shell's narrow tier: the sixth view (Plan)
	ActTabNext      // the lower panel's next tab, wrapping (busmon-keys)
	ActTabPrev      // the lower panel's previous tab, wrapping
	ActAskOrWrite   // w: the open question, or with none the compose window to master
	ActNextItem     // reader: the next item of the list the page came from (A5)
	ActPrevItem     // reader: the previous one
	ActAskOther     // the shell's ask: a free answer ("Other") in the compose window
	ActFold         // Requests: show or hide the answered and older threads
	ActTour         // the help: the first-launch tour again
	ActComplete     // the line: complete the @agent (the field does it)
	ActServiceLog   // Services (F3): the selected service's log
	ActPlanStart    // a plan task waiting for the person: write to master that they start it
	ActTokens       // open the tokens page (U)
	ActLineEdit     // the line: move its cursor or delete at it (the field does it)
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
	OnPage bool      // advertised in the footer only on a page that offers it (footerHintsOn's offers)
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
	notActivity   = []PanelID{PanelAgents, PanelBoard, PanelRequests, PanelPlan}
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
		{Keys: GlyphArrowL + GlyphArrowR, Hint: "tab", Help: "the lower panel's previous or next tab (Activity, Board, Requests, Plan), wrapping", Act: ActTabNext, Panels: lowerTabs, match: keyIs(keyRight)},
		{Keys: GlyphArrowL, Act: ActTabPrev, Panels: lowerTabs, match: keyIs(keyLeft)},
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
		{Keys: "!", Hint: "answer", Alt: "F4", Act: ActAsk, match: either(keyIs(keyF4), runeIs('!'))},
		{Keys: "w", Help: "answer the open question, or with none open write to master", Act: ActAskOrWrite, match: runeIs('w')},
		{Keys: "d del", Hint: "remove", Act: ActDelete, Panels: []PanelID{PanelAgents, PanelRequests}, match: either(runeIs('d'), keyIs(keyDelete))},
		{Keys: "a", Help: "show or hide the agents silent for more than a day", Act: ActShowAll, Panels: []PanelID{PanelAgents}, match: runeIs('a')},
		{Keys: "o", Help: "Requests: show or hide the answered threads and those older than 24 h", Act: ActFold, Panels: []PanelID{PanelRequests}, match: runeIs('o')},
		{Keys: "/", Hint: "filter", Act: ActFilter, match: runeIs('/')},
		{Keys: "i", Hint: "write", Alt: ":", Act: ActCompose, match: runeIs('i', ':')},
		{Keys: "@", Act: ActComposeAt, match: runeIs('@')},
		{Keys: "I", Help: "write in the compose window (several lines, Ctrl+S sends)", Act: ActComposeOpen, match: runeIs('I')},
		{Keys: "esc", Hint: "back", Act: ActEscape, match: keyIs(keyEscape)},
		{Keys: "F2", Hint: "requests", Alt: "r", Act: ActRequests, match: either(keyIs(keyF2), runeIs('r'))},
		{Keys: "F3", Hint: "services", Alt: "s", Act: ActServices, match: either(keyIs(keyF3), runeIs('s'))},
		{Keys: "R", Help: "where was I? open the project recap", Act: ActRecap, match: runeIs('R')},
		{Keys: "P", Help: "the batch's plan: its tasks by state (enter opens one, or expands done)", Act: ActPlan, match: runeIs('P')},
		{Keys: "U", Help: "tokens: the account windows, every project side by side, this project by agent", Act: ActTokens, match: runeIs('U')},
		{Keys: "F1", Hint: "help", Alt: "?", Act: ActHelp, match: either(keyIs(keyF1), runeIs('?'))},
		{Keys: "q", Hint: "quit", Act: ActQuit, match: runeIs('q')},
	}
	// shellTabs: in the shell, 1-3 pick the tabbed panel's tab at every
	// width; the narrow tier's five views take 1-5 (For you first).
	shellTabs = []binding{
		{Keys: "1-4", Hint: "tabs", Act: ActTab1, Tiers: []Tier{TierWide, TierMedium}, match: runeIs('1')},
		{Keys: "1-6", Hint: "view", Act: ActTab1, Tiers: []Tier{TierNarrow}, match: runeIs('1')},
		{Keys: "2", Act: ActTab2, match: runeIs('2')},
		{Keys: "3", Act: ActTab3, match: runeIs('3')},
		{Keys: "4", Act: ActTab4, match: runeIs('4')},
		{Keys: "5", Act: ActTab5, Tiers: []Tier{TierNarrow}, match: runeIs('5')},
		{Keys: "6", Act: ActTab6, Tiers: []Tier{TierNarrow}, match: runeIs('6')},
	}
	shellBindings = shellPanelBindings()
	// shellAskBindings: in the shell's reader a digit answers with that option,
	// as a click on it does (busmon Charm spec, "For you"), and o is "Other".
	shellAskBindings = shellAskTable()
	inputBindings    = []binding{
		{Keys: "^O", Hint: "several lines", Alt: "alt-enter", Act: ActComposeOpen, match: either(keyIs(keyCtrlO), altEnter)},
		{Keys: "enter", Hint: "send", Act: ActSend, match: keyIs(keyEnter)},
		{Keys: "tab", Hint: "complete @agent", Act: ActComplete, match: keyIs(keyTab)},
		{Keys: "esc", Hint: "back (draft kept)", Act: ActEscape, match: keyIs(keyEscape)},
		lineEditBinding,
	}
	filterBindings = []binding{
		{Keys: "enter", Hint: "apply", Act: ActSend, match: keyIs(keyEnter)},
		{Keys: "esc", Hint: "cancel", Act: ActEscape, match: keyIs(keyEscape)},
		lineEditBinding,
	}
	// lineEditBinding: both lines edit at their cursor (busmon-input-cursor);
	// the help lists it, the footer does not.
	lineEditBinding = binding{Keys: GlyphArrowL + GlyphArrowR + " home end",
		Help: "move the cursor by character or to either end (^A ^E too); del deletes at it, ^W back to a word, ^U to the start",
		Act:  ActLineEdit, match: keyIs(keyLeft, keyRight, keyHome, keyEnd, keyCtrlA, keyCtrlE, keyDelete, keyCtrlW, keyCtrlU)}
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
		{Keys: "Y", Hint: "copy thread", Help: "copy the whole thread (a message's page) or the whole report (an agent's page)", Act: ActCopyThread, OnPage: true, match: runeIs('Y')},
		{Keys: "n p " + GlyphArrowL + GlyphArrowR, Hint: "next/prev", Help: "the next or previous item of the list the page was opened from, as it was then (reopen to see newer lines)", Act: ActNextItem, match: either(runeIs('n'), keyIs(keyRight))},
		{Keys: "p", Act: ActPrevItem, match: either(runeIs('p'), keyIs(keyLeft))},
		{Keys: "enter ! w", Help: "answer the agent's open questions (w with none open: write to master)", Act: ActAsk, match: either(keyIs(keyEnter), keyIs(keyF4), runeIs('!', 'w'))},
		{Keys: "t", Help: "in the help: the first-launch tour again", Act: ActTour, match: runeIs('t')},
		{Keys: "enter s", Hint: "start", Help: "on a plan task waiting for you: start the discussion with master (the compose window, prefilled)", Act: ActPlanStart, OnPage: true, match: runeIs('s')}, // enter: through ActAsk's row, on that page
		{Keys: "a", Hint: "all", Help: "Health (F5): show or hide the fine checks (enter opens a check in place, y copies a For-you check's command)", Act: ActShowAll, OnPage: true, match: runeIs('a')},
		{Keys: "l", Help: "Services (F3): the selected service's log (enter too; " + arrows + " choose the card, " + GlyphArrowL + GlyphArrowR + " too when side by side)", Act: ActServiceLog, match: runeIs('l')},
		{Keys: "esc q", Hint: "close", Act: ActClose, match: either(keyIs(keyEscape), runeIs('q'))},
	}
	tourBindings = []binding{
		{Keys: "enter", Hint: "next", Alt: GlyphArrowR, Act: ActNextItem, match: either(keyIs(keyEnter), keyIs(keyRight))},
		{Keys: GlyphArrowL, Hint: "back", Act: ActPrevItem, match: keyIs(keyLeft)},
		{Keys: "esc", Hint: "skip", Act: ActClose, match: keyIs(keyEscape)},
	}
)

// shellPanelBindings is the panel table for the shell: the same rows, in the
// same order, with the shell's tabs in place of the panel table's, and the last-row key in For
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
	case ModeTour:
		bs = tourBindings
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

// footerHints renders the advertised bindings of the mode, in table order
// with the function keys grouped last in ascending order (A7), as whole
// "keys hint" pairs; a pair that does not fit is left out, never cut.
func footerHints(m Mode, p PanelID, t Tier, width int) Line {
	return footerHintsOn(m, p, t, width, nil, nil)
}

// footerHintsOn is footerHints on a page: a binding marked OnPage is
// advertised only when offers says the page offers its action (nil: none is),
// under the word hint gives for it on that page ("" or nil: its own).
func footerHintsOn(m Mode, p PanelID, t Tier, width int, offers func(Action) bool, hint func(Action) string) Line {
	var shown, fkeys []binding
	for _, b := range bindings(m) {
		if b.Hint == "" || !b.applies(p, t) || b.OnPage && (offers == nil || !offers(b.Act)) {
			continue
		}
		if fkey(b.Keys) {
			fkeys = append(fkeys, b)
		} else {
			shown = append(shown, b)
		}
	}
	sort.SliceStable(fkeys, func(i, j int) bool { return fkeyNumber(fkeys[i].Keys) < fkeyNumber(fkeys[j].Keys) })
	pair := func(b binding) Line {
		word := b.Hint
		if hint != nil {
			if w := hint(b.Act); w != "" {
				word = w
			}
		}
		return Line{{Text: "   "}, {Text: b.Keys, Role: RoleAccent, Bold: true}, {Text: " " + word, Role: RoleMuted}}
	}
	// Where the footer offers F1 help (the main screen), room is kept for it
	// and for F5 health (the header's health pill points to it), so a narrow
	// footer drops ordinary keys first; the other F-keys fill what is left.
	// A mode without F1 (an overlay) keeps its own keys, close among them.
	kept := func(b binding) bool { return false }
	if len(fkeys) > 0 && fkeyNumber(fkeys[0].Keys) == 1 {
		kept = func(b binding) bool { n := fkeyNumber(b.Keys); return n == 1 || n == 5 }
	}
	reserve := 0
	for _, b := range fkeys {
		if kept(b) {
			reserve += pair(b).Width()
		}
	}
	l := Line{{Text: " "}}
	for _, b := range shown {
		if p := pair(b); l.Width()+p.Width()+reserve <= width {
			l = append(l, p...)
		}
	}
	room := width - l.Width() - reserve // what the other F-keys may take
	var group Line
	for _, b := range fkeys {
		p := pair(b)
		if !kept(b) {
			if p.Width() > room {
				continue
			}
			room -= p.Width()
		}
		if l.Width()+group.Width()+p.Width() <= width {
			group = append(group, p...)
		}
	}
	l = append(l, group...)
	if len(l) > 1 && l[1].Text == "   " {
		l = append(Line{{Text: " "}}, l[2:]...) // no gap before the first pair
	}
	return FitLine(l, width)
}

// fkey reports a binding named by a function key alone ("F1".."F12").
func fkey(keys string) bool { return fkeyNumber(keys) > 0 }

// fkeyNumber is n for "Fn", else 0.
func fkeyNumber(keys string) int {
	if n, err := strconv.Atoi(strings.TrimPrefix(keys, "F")); err == nil && strings.HasPrefix(keys, "F") && n > 0 {
		return n
	}
	return 0
}

// altEnter is Alt+Enter (most terminals send it as Esc then Enter, which
// bubbletea reports as Enter with the Alt modifier).
func altEnter(k Key) bool { return k.Code == keyEnter && k.Alt }
