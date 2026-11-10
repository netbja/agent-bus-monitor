package main

import (
	"slices"
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
	ModeSettings // the settings page (C): its groups and their keys (settings_ui.go)
	// ModeNotifySetup is the guided notifications setup (notify_setup.go): its
	// own table, so q, t, s and r mean nothing of another page's there.
	ModeNotifySetup
	// ModeProjects is the Projects page (M, projects_admin.go): its own table.
	ModeProjects
	// ModeProjectDelete is the Projects page's delete confirmation
	// (projects_delete.go): its own small table, the name field's keys.
	ModeProjectDelete
	// ModeComposeFile is ModeCompose with the @ popup open over the text
	// (compose-at-file §4.4): its own table first, any other key the window's.
	ModeComposeFile
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
	ActTriage // T: walk what waits for the person (busmon-queue-triage §4)
	ActAsk
	ActAskNext
	ActAskPrev
	ActAskPick
	ActComposeOpen  // open the compose window
	ActComposeField // the window: text <-> recipient
	ActComposeAsk   // the window: answer the open question in its panel
	ActPasteImage   // the window: paste the clipboard's image (saved locally, its path inserted)
	ActUndo         // the window: take back the last edit (a paste, a run of typing)
	ActRedo         // the window: do again what undo took back
	ActRecap        // open the deterministic project recap
	ActHealth       // open project health details
	ActPickType     // the picker: a typed character or backspace edits the filter
	ActPlan         // open the batch's plan: the drawer (p, P; home-inbox-drawer §4.4)
	ActShowDone     // the plan board: show or fold DONE (d)
	ActTab5         // the shell's narrow tier: the fifth view
	ActTab6         // the shell's narrow tier: the sixth view (Plan)
	ActTabNext      // the lower panel's next tab, wrapping (busmon-keys)
	ActFieldNext    // the notifications setup: the next field (tab)
	ActFieldPrev    // the notifications setup: the field before (shift-tab)
	ActShowCode     // the notifications setup: the code alone, until any key (c)
	ActTestAgain    // the notifications setup: send the test again (t)
	ActEditSetup    // the notifications setup: back to the first step, values kept (e)
	ActRemoveTarget // a target's page: remove it, y/n (x)
	ActRenameTarget // a target's page: rename it (n)
	ActShowTopic    // an ntfy target's page: its topic and code (s)
	ActShowFormat   // the setup's first screen: the file's format, to write it by hand (?)
	ActTabPrev      // the lower panel's previous tab, wrapping
	ActAskOrWrite   // w: the open question, or with none the compose window to master
	ActSleep        // Z: suspend the open project, or resume it when it is suspended (agentbus suspend/resume)
	ActNextItem     // reader: the next item of the list the page came from (A5)
	ActPrevItem     // reader: the previous one
	ActAskOther     // the shell's ask: a free answer ("Other") in the compose window
	ActFold         // Requests: show or hide the answered and older threads
	ActTour         // the help: the first-launch tour again
	ActComplete     // the line: complete the @agent (the field does it)
	ActServiceLog   // Services (F3): the selected service's log
	ActPlanStart    // a plan task waiting for the person: write to master that they start it
	ActTokens       // open the tokens page (U)
	ActSettings     // open the settings page (C)
	ActHistory      // open the history page (H)
	ActAbout        // open About (A): the versions and the bus line
	ActReplay       // About: play the packet's run again (r)
	ActOlder        // History: load the days before
	ActLineEdit     // the line: move its cursor or delete at it (the field does it)
	ActGroupNext    // settings (C): → into the keys (or the next tab on a narrow page)
	ActGroupPrev    // settings (C): ← back to the groups (or the previous tab on a narrow page)
	ActFocusToggle  // settings (C): tab moves the focus between the groups and the keys
	ActReset        // settings (C): remove the selected key from config.toml (asks y)
	ActResetGroup   // settings (C): remove the group's keys from config.toml (asks y)
	ActPriorityUp   // Requests or a request's page: raise a tracked request's priority (the person's)
	ActPriorityDown // and lower it
	ActProjects     // open the Projects page (M)
	ActStopServices // projects (M): stop the selected project's services here (agentbus down)
	ActBackupNow    // projects (M): back the selected project up (agentbus backup --project)
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

// never is a help-only row's match: the key is handled outside the table.
func never(Key) bool { return false }

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
	notActivity   = []PanelID{PanelAgents, PanelBoard, PanelRequests, PanelWork}
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
		{Keys: GlyphArrowL + GlyphArrowR, Hint: "tab", Help: "the lower panel's previous or next tab (Activity, Board, Work), wrapping", Act: ActTabNext, Panels: tabbedPanels, match: keyIs(keyRight)},
		{Keys: GlyphArrowL, Act: ActTabPrev, Panels: tabbedPanels, match: keyIs(keyLeft)},
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
		{Keys: "T", Hint: "triage", Help: "triage: open what waits for you one item at a time, the next after each answer or send (n next, p back, esc ends); not t, the tour", Act: ActTriage, match: runeIs('T')},
		{Keys: "!", Hint: "answer", Alt: "F4", Act: ActAsk, match: either(keyIs(keyF4), runeIs('!'))},
		{Keys: "w", Help: "answer the open question, or with none open write to master", Act: ActAskOrWrite, match: runeIs('w')},
		{Keys: "Z", Help: "put the project to sleep (agentbus suspend), or, when it is suspended, bring it back (agentbus resume); asks first", Act: ActSleep, match: runeIs('Z')},
		{Keys: "d del", Hint: "remove", Act: ActDelete, Panels: []PanelID{PanelAgents, PanelRequests}, match: either(runeIs('d'), keyIs(keyDelete))},
		{Keys: "a", Help: "show or hide the agents silent for more than a day", Act: ActShowAll, Panels: []PanelID{PanelAgents}, match: runeIs('a')},
		{Keys: "o", Help: "Requests: show or hide the answered threads and those older than 7 days", Act: ActFold, Panels: []PanelID{PanelRequests}, match: runeIs('o')},
		// request-priority §6: on one tracked request, written at once (the opposite key undoes it).
		{Keys: "+ -", Hint: "priority", Help: "Requests: raise or lower a tracked request's priority (low, normal, high); agents take the high ones first", Act: ActPriorityUp, Panels: []PanelID{PanelRequests, PanelWork}, OnPage: true, match: runeIs('+')},
		{Keys: "-", Act: ActPriorityDown, Panels: []PanelID{PanelRequests, PanelWork}, OnPage: true, match: runeIs('-')},
		{Keys: "/", Hint: "filter", Act: ActFilter, match: runeIs('/')},
		{Keys: "i", Hint: "write", Alt: ":", Act: ActCompose, match: runeIs('i', ':')},
		{Keys: "@", Act: ActComposeAt, match: runeIs('@')},
		{Keys: "I", Help: "write in the compose window (several lines, Ctrl+S sends)", Act: ActComposeOpen, match: runeIs('I')},
		{Keys: "esc", Hint: "back", Act: ActEscape, match: keyIs(keyEscape)},
		{Keys: "F2", Hint: "requests", Alt: "r", Act: ActRequests, match: either(keyIs(keyF2), runeIs('r'))},
		{Keys: "F3", Hint: "services", Alt: "s", Act: ActServices, match: either(keyIs(keyF3), runeIs('s'))},
		{Keys: "R", Help: "where was I? open the project recap", Act: ActRecap, match: runeIs('R')},
		{Keys: "p P", Hint: "plan", Help: "the batch's plan in a drawer: NOW, NEXT, LATER, DONE, the progress; enter opens a task, d shows done, esc closes", Act: ActPlan, match: runeIs('p', 'P')},
		{Keys: "d", Hint: "done", Help: "Work: the plan with its done tasks (the drawer; d there folds them)", Act: ActShowDone, Panels: []PanelID{PanelWork}, match: runeIs('d')},
		{Keys: "U", Help: "tokens: the account windows, every project side by side, this project by agent", Act: ActTokens, match: runeIs('U')},
		{Keys: "C", Help: "settings: display, attention and your name, changed in config.toml", Act: ActSettings, match: runeIs('C')},
		{Keys: "M", Help: "projects: every project of this host, its state, agents, keys, services and herdr workspace; sleep, stop or back one up", Act: ActProjects, match: runeIs('M')},
		{Keys: "H", Help: "history: re-read the past, the archive on this host then the live streams (enter opens an entry)", Act: ActHistory, match: runeIs('H')},
		{Keys: "A", Help: "about: busmon's, agentbus's and Redis's versions (y copies them), and the bus at work", Act: ActAbout, match: runeIs('A')},
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
		{Keys: "4", Help: "Work, on NEXT: the plan's next tasks (key 4 was the Plan tab)", Act: ActTab4, Tiers: []Tier{TierWide, TierMedium}, match: runeIs('4')},
		{Keys: "4", Act: ActTab4, Tiers: []Tier{TierNarrow}, match: runeIs('4')},
		{Keys: "5", Act: ActTab5, Tiers: []Tier{TierNarrow}, match: runeIs('5')},
		{Keys: "6", Help: "Work, on NEXT: the plan's next tasks", Act: ActTab6, Tiers: []Tier{TierNarrow}, match: runeIs('6')},
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
		{Keys: "Ctrl+S", Hint: "send", Act: ActSend, match: keyIs(keyCtrlS)},
		{Keys: "tab", Hint: "recipient", Help: "recipient " + GlyphNone + " text; a text that is only @prefix becomes the recipient (completed, or its list opens)", Act: ActComposeField, match: keyIs(keyTab)},
		{Keys: "Ctrl+T", Help: "after the open-question hint: answer in its question panel", Act: ActComposeAsk, match: keyIs(keyCtrlT)},
		{Keys: "Ctrl+V", Help: "paste the clipboard's image (saved 0600 under the cache's agentbus/attachments, its path inserted once; nothing uploaded), else its text (speech-to-text tools paste this way)", Act: ActPasteImage, match: keyIs(keyCtrlV)},
		{Keys: "Ctrl+Z", Help: "undo the last edit: a paste whole, a run of typing or of deleting (until the window closes)", Act: ActUndo, match: keyIs(keyCtrlZ)},
		{Keys: "Ctrl+Y", Help: "redo what undo took back (also ctrl+shift+z where the terminal tells it apart)", Act: ActRedo, match: keyIs(keyCtrlY)},
		{Keys: "esc", Hint: "close (draft kept)", Act: ActClose, match: keyIs(keyEscape)},
		// Help only: the window's own keys move and select (tea_compose.go), never through the table.
		{Keys: "shift+" + arrows + GlyphArrowL + GlyphArrowR + " home end", Help: "select text: del or backspace removes it, typing replaces it, esc or a plain move clears it", match: never},
		{Keys: "pgup pgdn", Help: "a page up or down (with shift: extend the selection)", match: never},
	}
	// composeFileBindings: the @ popup over the compose window's text
	// (compose-at-file §4.4). A key it does not bind goes to the window's
	// table and the text, and the query follows it.
	composeFileBindings = []binding{
		{Keys: arrows, Hint: "choose", Act: ActUp, match: keyIs(keyUp)},
		{Keys: GlyphDownArrow, Act: ActDown, match: keyIs(keyDown)},
		{Keys: "pgup pgdn", Help: "a page of the file list", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "enter", Hint: "insert", Help: "insert the selected path, a file or a folder, in place of the @ and its query (no new line)", Act: ActOpen, match: keyIs(keyEnter)},
		{Keys: "tab", Hint: "open folder", Help: "on a folder: list its files (the query becomes the folder); on a file: insert it; in a message that holds only @ and the start of an agent's name, the recipient instead (the hint says recipient)", Act: ActFieldNext, match: keyIs(keyTab)},
		{Keys: "esc", Hint: "keep text", Help: "close the list only, the typed @query kept; esc again closes the window", Act: ActEscape, match: keyIs(keyEscape)},
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
		{Keys: arrows + " j k", Hint: "scroll", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: "pgup", Help: "page up", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Help: "page down", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "home g", Help: "first row", Act: ActFirst, match: either(keyIs(keyHome), runeIs('g'))},
		{Keys: "end G", Help: "last row", Act: ActLast, match: either(keyIs(keyEnd), runeIs('G'))},
		{Keys: "y", Hint: "copy", Act: ActCopy, match: runeIs('y')},
		{Keys: "Y", Hint: "copy thread", Help: "copy the whole thread (a message's page) or the whole report (an agent's page, a recap item's)", Act: ActCopyThread, OnPage: true, match: runeIs('Y')},
		{Keys: "n p " + GlyphArrowL + GlyphArrowR, Hint: "next/prev", Help: "the next or previous item of the list the page was opened from, as it was then (reopen to see newer lines)", Act: ActNextItem, match: either(runeIs('n'), keyIs(keyRight))},
		{Keys: "p", Act: ActPrevItem, match: either(runeIs('p'), keyIs(keyLeft))},
		{Keys: "enter ! w", Help: "answer the agent's open questions (w with none open: write to master)", Act: ActAsk, match: either(keyIs(keyEnter), keyIs(keyF4), runeIs('!', 'w'))},
		{Keys: "t", Help: "in the help: the first-launch tour again; in History (H): the selected entry's thread", Act: ActTour, match: runeIs('t')},
		{Keys: "/", Hint: "filter", Help: "History (H): filter by @agent, a thread id or words", Act: ActFilter, OnPage: true, match: runeIs('/')},
		{Keys: "enter s", Hint: "start", Help: "on a plan task waiting for you: start the discussion with master (the compose window, prefilled)", Act: ActPlanStart, OnPage: true, match: runeIs('s')}, // enter: through ActAsk's row, on that page
		{Keys: "a", Hint: "all", Help: "Health (F5): show or hide the fine checks (enter opens a check in place, y copies a For-you check's command)", Act: ActShowAll, OnPage: true, match: runeIs('a')},
		{Keys: "o", Hint: "older", Help: "History (H): load the three days before; the recap (R): show For you's items older than a day", Act: ActOlder, OnPage: true, match: runeIs('o')},
		{Keys: "r", Hint: "replay", Help: "About (A): play the packet's run again", Act: ActReplay, OnPage: true, match: runeIs('r')},
		{Keys: "d", Hint: "done", Help: "the plan (p): show or fold the done tasks", Act: ActShowDone, OnPage: true, match: runeIs('d')},
		{Keys: "l", Help: "Services (F3): the selected service's log (enter too; " + arrows + " choose the card, " + GlyphArrowL + GlyphArrowR + " too when side by side)", Act: ActServiceLog, match: runeIs('l')},
		{Keys: "esc q", Hint: "close", Act: ActClose, match: either(keyIs(keyEscape), runeIs('q'))},
		// After close: on a narrow footer the way out stays (message-reply-key).
		{Keys: "+ -", Hint: "priority", Help: "a tracked request's page: raise or lower its priority (low, normal, high)", Act: ActPriorityUp, OnPage: true, match: runeIs('+')},
		{Keys: "-", Act: ActPriorityDown, OnPage: true, match: runeIs('-')},
		// r is shared with About's replay: lookupOn takes the one the page offers (reply-key-r).
		{Keys: "r", Hint: "reply", Alt: "i I", Help: "reply in the compose window: on an agent's page to that agent; on a message, report or note page to its sender, an agent, prefilled with re: and its first line", Act: ActComposeOpen, OnPage: true, match: runeIs('r', 'i', 'I')},
	}
	// settingsBindings: the C page (settings-panels, settings-focus). The focus
	// is on the groups or on the keys: on the groups ↑↓ choose one (the keys
	// follow), → or enter go to its keys; on the keys ↑↓ move, ← goes back,
	// enter edits. On a narrow page the groups are a tab bar: ←→ step it, ↓ or
	// enter go to the keys, ↑ on the first key comes back. tab toggles the
	// focus. An open choice takes ←→ for its value first (settings_ui.go).
	settingsBindings = []binding{
		{Keys: arrows + " j k", Hint: "move", Help: "the groups (or the keys, with the focus there); on a narrow page " + GlyphDownArrow + " from the tabs goes to the keys, " + GlyphUp + " on the first key back", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: GlyphArrowL + GlyphArrowR, Hint: "panel", Help: GlyphArrowR + " into the keys, " + GlyphArrowL + " back to the groups (a narrow page: they step the tabs); on an open choice they change its value", Act: ActGroupNext, match: keyIs(keyRight)},
		{Keys: GlyphArrowL, Act: ActGroupPrev, match: keyIs(keyLeft)},
		{Keys: "tab", Hint: "focus", Help: "move the focus between the groups and the keys", Act: ActFocusToggle, match: either(keyIs(keyTab), keyIs(keyBacktab))},
		{Keys: "enter", Hint: "edit", Help: "on a group: go to its keys; on a key: flip an on/off, open a choice or the line editor (enter writes, esc leaves the file as it is; a number or a duration takes only its digits and units, + and - step it)", Act: ActOpen, match: either(keyIs(keyEnter), runeIs(' '))},
		{Keys: "d", Hint: "reset", Help: "remove the selected key from config.toml (y confirms): what applies next is named", Act: ActReset, match: runeIs('d')},
		{Keys: "D", Hint: "reset group", Help: "remove every key of the group set in config.toml (y confirms); your own backup.after_* stay", Act: ActResetGroup, match: runeIs('D')},
		{Keys: "esc q", Hint: "back", Act: ActClose, match: either(keyIs(keyEscape), runeIs('q'))},
	}
	// notifySetupBindings: the guided setup (notify-guided-setup §5.2). A field
	// being edited takes every key but tab, enter and esc (notifyFieldKey).
	notifySetupBindings = []binding{
		{Keys: "enter", Hint: "next", Help: "the next step, or confirm the choice; on a field: edit it", Act: ActOpen, match: keyIs(keyEnter)},
		{Keys: "esc", Hint: "back", Help: "back one step; on step 1 or a target's page, close (nothing is written before the write step)", Act: ActClose, match: keyIs(keyEscape)},
		{Keys: arrows, Hint: "choose", Help: "between choices, or between fields", Act: ActUp, match: keyIs(keyUp)},
		{Keys: GlyphDownArrow, Act: ActDown, match: keyIs(keyDown)},
		{Keys: "tab", Hint: "field", Help: "the next field (shift-tab the one before), edited at once", Act: ActFieldNext, match: keyIs(keyTab)},
		{Keys: "shift-tab", Act: ActFieldPrev, match: keyIs(keyBacktab)},
		{Keys: "y", Hint: "copy topic", Help: "ntfy: copy the topic (the subscribe step, a target's page)", Act: ActCopy, match: runeIs('y')},
		{Keys: "Y", Help: "ntfy: copy the subscribe link", Act: ActCopyThread, match: runeIs('Y')},
		{Keys: "c", Hint: "code", Help: "ntfy: the code alone, full screen, until any key", Act: ActShowCode, match: runeIs('c')},
		{Keys: "t", Hint: "test", Help: "send the test again (after the write, a target's page)", Act: ActTestAgain, match: runeIs('t')},
		{Keys: "e", Hint: "edit", Help: "back to the first step, values kept", Act: ActEditSetup, match: runeIs('e')},
		{Keys: "x", Hint: "remove", Help: "a target's page: remove it (y/n; no other key while it asks)", Act: ActRemoveTarget, match: runeIs('x')},
		{Keys: "n", Hint: "rename", Help: "a target's page: rename it (enter writes)", Act: ActRenameTarget, match: runeIs('n')},
		{Keys: "s", Hint: "topic", Help: "an ntfy target's page: its topic and code, until any key", Act: ActShowTopic, match: runeIs('s')},
		{Keys: "?", Hint: "format", Help: "the first step: notify.toml's format and a table to copy, to write the file by hand", Act: ActShowFormat, match: runeIs('?')},
		{Keys: "F1", Hint: "help", Act: ActHelp, match: keyIs(keyF1)},
	}
	// projectsBindings: the Projects page (M, projects-admin §7).
	projectsBindings = []binding{
		{Keys: arrows + " j k", Hint: "move", Act: ActUp, match: either(keyIs(keyUp), runeIs('k'))},
		{Keys: "j", Act: ActDown, match: either(keyIs(keyDown), runeIs('j'))},
		{Keys: "pgup pgdn", Help: "a page up or down", Act: ActPageUp, match: keyIs(keyPgUp)},
		{Keys: "pgdn", Act: ActPageDown, match: keyIs(keyPgDn)},
		{Keys: "home end", Help: "the first or the last project", Act: ActFirst, match: keyIs(keyHome)},
		{Keys: "end", Act: ActLast, match: keyIs(keyEnd)},
		{Keys: "enter", Hint: "detail", Help: "the selected project: every column, its streams, its pause or suspend record, its template and workspace", Act: ActOpen, match: keyIs(keyEnter)},
		{Keys: "Z", Hint: "sleep", Help: "put the selected project to sleep (agentbus suspend), or wake it when it is suspended (resume); asks first", Act: ActSleep, match: runeIs('Z')},
		{Keys: "x", Hint: "stop", Help: "stop the selected project's services on this machine (agentbus down; deletes nothing); asks first", Act: ActStopServices, match: runeIs('x')},
		{Keys: "b", Hint: "back up", Help: "back the selected project up now (agentbus backup --project); the archive is the notice", Act: ActBackupNow, match: runeIs('b')},
		{Keys: "d del", Hint: "delete", Help: "delete the selected project's keys (refused while it is live; a backup offered first, its name typed)", Act: ActDelete, match: either(runeIs('d'), keyIs(keyDelete))},
		{Keys: "R", Hint: "read again", Help: "read every project again (the page is never polled)", Act: ActRecap, match: runeIs('R')},
		{Keys: "esc", Hint: "back", Act: ActClose, match: keyIs(keyEscape)},
		{Keys: "F1", Hint: "help", Alt: "?", Act: ActHelp, match: either(keyIs(keyF1), runeIs('?'))},
		{Keys: "q", Hint: "quit", Act: ActQuit, match: runeIs('q')},
	}
	// deleteBindings: the delete confirmation (projects-admin §5.2, §7). The
	// name step takes every printable key into its field (deleteKey), so a d
	// typed there is a letter.
	deleteBindings = []binding{
		{Keys: "b", Hint: "back up", Help: "the first step: back the project up, then go on (only when it succeeds)", Act: ActBackupNow, match: runeIs('b')},
		{Keys: "n", Hint: "no backup", Help: "the first step: go on without a backup", Act: ActNone, match: runeIs('n')},
		{Keys: "enter", Hint: "confirm", Help: "the name step: delete when the field is the project's name, byte for byte", Act: ActOpen, match: keyIs(keyEnter)},
		{Keys: "y", Hint: "delete", Help: "the open project: the second confirmation; busmon exits after the delete", Act: ActConfirm, match: runeIs('y')},
		{Keys: "esc", Hint: "cancel", Help: "cancel at every step; nothing is written", Act: ActClose, match: keyIs(keyEscape)},
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
			out = append(out, b, binding{Keys: "o", Help: "Other: a free answer in the compose window", Act: ActAskOther, match: runeIs('o')},
				binding{Keys: "n p", Help: "during triage (T): the next or previous item", Act: ActNextItem, match: runeIs('n')},
				binding{Keys: "p", Act: ActPrevItem, match: runeIs('p')})
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
	case ModeComposeFile:
		bs = composeFileBindings
	case ModePick:
		bs = pickBindings
	case ModeShell:
		bs = shellBindings
	case ModeShellAsk:
		bs = shellAskBindings
	case ModeTour:
		bs = tourBindings
	case ModeSettings:
		bs = settingsBindings
	case ModeNotifySetup:
		bs = notifySetupBindings
	case ModeProjects:
		bs = projectsBindings
	case ModeProjectDelete:
		bs = deleteBindings
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

// lookupOn is lookup on a page: of the bindings that match ev, the first that
// is not OnPage or that the page offers (offers); when the page offers none of
// them, the last one, so a key shared by two pages' actions (r: About's replay,
// a message's reply) still reaches the handler that says why.
func lookupOn(m Mode, p PanelID, t Tier, ev Key, offers func(Action) bool) Action {
	last := ActNone
	for _, b := range bindings(m) {
		if !b.applies(p, t) || !b.match(ev) {
			continue
		}
		if !b.OnPage || offers == nil || offers(b.Act) {
			return b.Act
		}
		last = b.Act
	}
	return last
}

// footerHints renders the advertised bindings of the mode, in table order
// with the function keys grouped last in ascending order (A7), as whole
// "keys hint" pairs; a pair that does not fit is left out, never cut.
func footerHints(m Mode, p PanelID, t Tier, width int) Line {
	return footerHintsOn(m, p, t, width, nil, nil)
}

// footerHintsOn is footerHints on a page: a binding marked OnPage is
// advertised only when offers says the page offers its action (nil: none is),
// under the word hint gives for it on that page ("" or nil: its own). On the
// main screen (ModeShell) the enter pair comes first, and a rowNoPair word
// leaves it out.
func footerHintsOn(m Mode, p PanelID, t Tier, width int, offers func(Action) bool, hint func(Action) string) Line {
	var shown, fkeys []binding
	for _, b := range bindings(m) {
		if b.Hint == "" || !b.applies(p, t) || b.OnPage && (offers == nil || !offers(b.Act)) {
			continue
		}
		if b.Act == ActOpen && hint != nil && hint(b.Act) == rowNoPair {
			continue // Enter does nothing on this row: no pair (busmon-row-actions)
		}
		switch {
		case fkey(b.Keys):
			fkeys = append(fkeys, b)
		case b.Act == ActOpen && m == ModeShell:
			// The main screen: the selected row's own act comes first
			// (busmon-queue-triage §5.2), then T, the way into the walk (§4.1).
			shown = append([]binding{b}, shown...)
		case b.Act == ActTriage && m == ModeShell:
			at := 0
			if len(shown) > 0 && shown[0].Act == ActOpen {
				at = 1
			}
			shown = slices.Insert(shown, at, b)
		case b.Act == ActPlan && m == ModeShell: // after T (home-inbox-drawer §7)
			at := slices.IndexFunc(shown, func(x binding) bool { return x.Act == ActTriage }) + 1
			shown = slices.Insert(shown, at, b)
		default:
			shown = append(shown, b)
		}
	}
	if m == ModeShell { // the priority keys right after the row's enter, as the Requests mock-ups place them
		if i := slices.IndexFunc(shown, func(x binding) bool { return x.Act == ActPriorityUp }); i > 0 {
			b, at := shown[i], 0
			if shown[0].Act == ActOpen {
				at = 1
			}
			shown = slices.Insert(slices.Delete(shown, i, i+1), at, b)
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
