package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// The command palette, slice 1 (spec docs/superpowers/specs/2026-11-13-busmon-command-palette-design.md):
// a leading / in the one-line input turns it into a command line, Ctrl+P
// opens it from the panels and the pages. Class A rows run a panel key's
// own action through runPanelAct (the path its key takes), class B rows
// write as the line does, and three rows (filter <words>, tour, set <key>)
// have a Run of their own. Class C (agentbus commands) is slice 2.

// palCmd is one palette command.
type palCmd struct {
	Name    string
	Aliases []string // a one-letter alias matches exactly and case-sensitively, as its key
	Summary string
	Args    []palArg
	Act     Action                              // class A: the key's action
	Run     func(m *Model, args []string) tea.Cmd // the others
	Key     Action                              // a Run row: the action whose key the row shows (0 none)
	KeyText string                              // a key written in the row (tour: the help page's t)
	// InLine: the row writes through the line itself (class B): the line
	// becomes "@agent text" and is sent as Enter sends it, its holds and
	// confirmations included (an open question, a pause).
	InLine bool
}

// palArg is one argument; Rest takes the rest of the line (a text).
type palArg struct {
	Name     string
	Rest     bool
	Optional bool
	Check    func(string) error
	Complete func(m *Model) []string
}

// paletteTable is the one table (§4.1, §4.2's aliases), in its order.
var paletteTable []palCmd

func init() {
	agentArg := palArg{Name: "agent", Check: func(s string) error {
		if !bus.ValidName(s) {
			return fmt.Errorf("%q is not an agent name", s)
		}
		return nil
	}, Complete: func(m *Model) []string { return m.names(false) }}
	text := palArg{Name: "text", Rest: true}
	keyArg := palArg{Name: "key", Optional: true, Check: func(s string) error {
		if _, ok := config.Lookup(s); !ok {
			return fmt.Errorf("no setting %q", s)
		}
		return nil
	}, Complete: func(*Model) []string {
		var out []string
		for _, k := range config.Keys {
			out = append(out, k.Name)
		}
		return out
	}}
	a := func(name, summary string, act Action, aliases ...string) palCmd {
		return palCmd{Name: name, Summary: summary, Act: act, Aliases: aliases}
	}
	paletteTable = []palCmd{
		a("requests", "who owes whom an answer", ActRequests, "f2", "r", "req"),
		a("services", "the project's services, their cards and logs", ActServices, "f3", "s", "svc"),
		a("health", "the project's measured checks", ActHealth, "f5"),
		a("help", "every key, and these commands", ActHelp, "f1", "?"),
		a("plan", "the plan's tasks", ActPlan, "p"),
		a("recap", "what happened while you were away", ActRecap, "R"),
		a("triage", "walk what waits for you", ActTriage, "T"),
		a("tokens", "tokens per project and agent", ActTokens, "U"),
		a("settings", "the settings page", ActSettings, "C", "config"),
		a("projects", "every project of this host", ActProjects, "M"),
		a("history", "the archive and the streams, newest first", ActHistory, "H"),
		a("about", "versions and the bus line", ActAbout, "A", "version"),
		a("unread", "the next unread item for you", ActNextUnread, "u"),
		a("follow", "auto-scroll on or off", ActFollow, "f", "autoscroll"),
		a("all", "show or hide the agents silent for a day", ActShowAll, "a"),
		a("compose", "the compose window", ActComposeOpen, "I"),
		a("sleep", "suspend or resume the project (asks first)", ActSleep, "Z"),
		{Name: "filter", Summary: "filter Activity by @agent, a thread or words", Act: ActNone, Key: ActFilter,
			Args: []palArg{{Name: "words", Rest: true, Optional: true}}, Run: (*Model).palFilter},
		a("answer", "answer an agent's open questions", ActAsk, "f4", "!"),
		{Name: "write", Summary: "a message to an agent (as @agent text)", Key: ActComposeAt, Aliases: []string{"@"},
			Args: []palArg{agentArg, text}, Run: (*Model).palWrite, InLine: true},
		{Name: "master", Summary: "a directive to master: ask for an agent's or master's action", Key: ActAskOrWrite,
			Args: []palArg{text}, Run: (*Model).palMaster, InLine: true},
		{Name: "set", Summary: "jump to a setting on the settings page", Key: ActSettings,
			Args: []palArg{keyArg}, Run: (*Model).palSet},
		{Name: "tour", Summary: "the first-launch tour again", KeyText: "F1 t", Aliases: []string{"t"}, Run: (*Model).palTour},
		a("quit", "leave busmon", ActQuit, "q", "exit"),
	}
}

// paletteAliasMatch: a one-letter alias is exact and case-sensitive (busmon
// pairs letters by case: r Requests, R recap); any other ignores case.
func paletteAliasMatch(alias, q string) bool {
	if len([]rune(alias)) == 1 {
		return alias == q
	}
	return strings.EqualFold(alias, q)
}

// paletteMatch ranks the table for q (§4.2): an exact name or alias, a name
// prefix, a word start in the name or summary, a subsequence of the name;
// ties keep the table's order. An empty q lists every command.
func paletteMatch(q string) []int {
	q = strings.TrimSpace(q)
	var tiers [4][]int
	for i, c := range paletteTable {
		lq := strings.ToLower(q)
		switch {
		case q == "":
			tiers[0] = append(tiers[0], i)
		case strings.EqualFold(c.Name, q) || anyAlias(c.Aliases, q):
			tiers[0] = append(tiers[0], i)
		case strings.HasPrefix(c.Name, lq):
			tiers[1] = append(tiers[1], i)
		case wordStart(c.Name+" "+strings.ToLower(c.Summary), lq):
			tiers[2] = append(tiers[2], i)
		case subsequence(c.Name, lq):
			tiers[3] = append(tiers[3], i)
		}
	}
	return append(append(append(tiers[0], tiers[1]...), tiers[2]...), tiers[3]...)
}

func anyAlias(aliases []string, q string) bool {
	for _, a := range aliases {
		if paletteAliasMatch(a, q) {
			return true
		}
	}
	return false
}

// wordStart: some word of s starts with q.
func wordStart(s, q string) bool {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

// subsequence: q's letters appear in s in order (rq in requests).
func subsequence(s, q string) bool {
	i := 0
	for _, r := range s {
		if i < len(q) && byte(r) == q[i] {
			i++
		}
	}
	return i == len(q) && q != ""
}

// paletteFind is the command a typed name or alias names, nil for none.
func paletteFind(name string) *palCmd {
	for i, c := range paletteTable {
		if c.Name == strings.ToLower(name) || anyAlias(c.Aliases, name) {
			return &paletteTable[i]
		}
	}
	return nil
}

var errNoCommand = errors.New("no command")

// parseCommandLine reads a command line (without its /): a name or an
// alias, then its arguments, each checked (§4.4). Nothing runs on an error.
func parseCommandLine(line string) (*palCmd, []string, error) {
	line = strings.TrimSpace(line)
	name, rest, _ := strings.Cut(line, " ")
	if name == "" {
		return nil, nil, errNoCommand
	}
	c := paletteFind(name)
	if c == nil {
		return nil, nil, fmt.Errorf("%w %q (/ alone lists them)", errNoCommand, name)
	}
	rest = strings.TrimSpace(rest)
	if len(c.Args) == 0 {
		if rest != "" {
			return nil, nil, fmt.Errorf("%s takes no argument", c.Name)
		}
		return c, nil, nil
	}
	var args []string
	for _, a := range c.Args {
		var v string
		if a.Rest {
			v, rest = rest, ""
		} else {
			v, rest, _ = strings.Cut(rest, " ")
			rest = strings.TrimSpace(rest)
		}
		if v == "" {
			if a.Optional {
				break
			}
			return nil, nil, fmt.Errorf("%s needs its %s", c.Name, a.Name)
		}
		if a.Check != nil {
			if err := a.Check(v); err != nil {
				return nil, nil, err
			}
		}
		args = append(args, v)
	}
	if rest != "" {
		return nil, nil, fmt.Errorf("%s: too many arguments", c.Name)
	}
	return c, args, nil
}

// paletteKeyHint is the key a row's command shares, derived from the panel
// table (§4.1): the first binding of its Act or Key. master shows w only
// while no ask is open: w answers one then.
func paletteKeyHint(c palCmd, asksOpen bool) string {
	if c.KeyText != "" {
		return c.KeyText
	}
	act := c.Act
	if act == ActNone {
		act = c.Key
	}
	if act == ActNone || act == ActAskOrWrite && asksOpen {
		return ""
	}
	for _, b := range panelBindings {
		if b.Act == act {
			keys := b.Keys
			if b.Alt != "" {
				keys += " " + b.Alt
			}
			return keys
		}
	}
	if act == ActQuit {
		return quitBinding.Keys
	}
	return ""
}

// runPalette runs c with its checked args: every row first leaves the line
// and closes every open page (§4.1), so a command started from a page shows
// its result, never the page.
func (m *Model) runPalette(c *palCmd, args []string) tea.Cmd {
	if c.InLine {
		m.closeReader()
		return c.Run(m, args)
	}
	m.leaveLine(false) // the command is spent; a message draft kept for i stays
	m.closeReader()
	if c.Run != nil {
		return c.Run(m, args)
	}
	return m.runPanelAct(c.Act)
}

// palFilter is filter <words>: what the filter line's Enter does, Activity
// focused; with no words, the filter line itself (as /).
func (m *Model) palFilter(args []string) tea.Cmd {
	if len(args) == 0 {
		return m.runPanelAct(ActFilter)
	}
	m.ui.filter = parseFilter(args[0])
	m.focusPanel(PanelActivity)
	return nil
}

// palWrite is write <agent> <text>: the line becomes @agent text and is
// sent as Enter sends it.
func (m *Model) palWrite(args []string) tea.Cmd {
	m.setLine("@"+args[0]+" "+args[1], len("@"+args[0]+" "+args[1]))
	return m.sendLine()
}

// palMaster is master <text>: @master text.
func (m *Model) palMaster(args []string) tea.Cmd {
	return m.palWrite([]string{"master", args[0]})
}

// palTour is the help page's t: the tour from its first page.
func (m *Model) palTour([]string) tea.Cmd {
	m.openTour(true)
	return nil
}

// palSet is set [key]: the settings page, on that key once its rows are read.
func (m *Model) palSet(args []string) tea.Cmd {
	cmd := m.openSettings()
	if len(args) == 1 {
		if p := m.settingsPageOpen(); p != nil {
			p.set.want = args[0]
		}
	}
	return cmd
}

// paletteOn: the line holds a command (a leading /, not //, §4.3).
func (m *Model) paletteOn() bool {
	return m.ui.line == lineMessage && strings.HasPrefix(m.ui.edit, "/") && !strings.HasPrefix(m.ui.edit, "//")
}

// paletteListOn: the list is shown (Esc hides it until the next edit).
func (m *Model) paletteListOn() bool { return m.paletteOn() && !m.ui.palHidden }

// paletteRows are the rows the typed command word matches.
func (m *Model) paletteRows() []int {
	word, _, _ := strings.Cut(strings.TrimPrefix(m.ui.edit, "/"), " ")
	return paletteMatch(word)
}

// paletteKey is a key on a command line; false leaves it to the line. ↑↓
// choose a row, Tab completes the name (or the argument being typed), Enter
// runs (the chosen row if ↑↓ moved since the last edit, else the typed line
// when it parses, else the chosen row), Esc hides the list, text kept.
func (m *Model) paletteKey(act Action, k Key) (tea.Cmd, bool) {
	if !m.paletteOn() {
		return nil, false
	}
	rows := m.paletteRows()
	m.ui.palSel = min(m.ui.palSel, max(0, len(rows)-1))
	switch {
	case act == ActEscape && !m.ui.palHidden:
		m.ui.palHidden = true
		return nil, true
	case (k.Code == keyUp || k.Code == keyDown) && len(rows) > 0 && !m.ui.palHidden:
		step := 1
		if k.Code == keyUp {
			step = len(rows) - 1
		}
		m.ui.palSel, m.ui.palMoved = (m.ui.palSel+step)%len(rows), true
		return nil, true
	case k.Code == keyTab:
		m.paletteComplete(rows)
		return nil, true
	case act == ActSend:
		return m.paletteEnter(rows), true
	}
	if m.editLine(k) {
		m.ui.palSel, m.ui.palMoved, m.ui.palHidden = 0, false, false
		return nil, true
	}
	return nil, false
}

// paletteEnter is Enter on a command line (§4.3).
func (m *Model) paletteEnter(rows []int) tea.Cmd {
	line := strings.TrimPrefix(strings.TrimSpace(m.ui.edit), "/")
	chosen := func() tea.Cmd {
		if len(rows) == 0 {
			m.ui.flash, m.ui.flashErr = "no command matches: / alone lists them", true
			return nil
		}
		_, rest, _ := strings.Cut(line, " ")
		c, args, err := parseCommandLine(strings.TrimSpace(paletteTable[rows[m.ui.palSel]].Name + " " + rest))
		if err != nil {
			m.ui.flash, m.ui.flashErr = err.Error(), true
			return nil
		}
		return m.runPalette(c, args)
	}
	if m.ui.palMoved {
		return chosen()
	}
	if c, args, err := parseCommandLine(line); err == nil {
		return m.runPalette(c, args)
	} else if !errors.Is(err, errNoCommand) {
		m.ui.flash, m.ui.flashErr = err.Error(), true // a known command, wrong arguments: said, nothing run
		return nil
	}
	return chosen()
}

// paletteComplete is Tab: the chosen name while the command word is typed,
// else the argument being typed, from its Complete (the first that starts
// with what is typed).
func (m *Model) paletteComplete(rows []int) {
	body := strings.TrimPrefix(m.ui.edit, "/")
	if !strings.Contains(body, " ") {
		if len(rows) > 0 {
			m.setLine("/"+paletteTable[rows[m.ui.palSel]].Name+" ", len("/"+paletteTable[rows[m.ui.palSel]].Name+" "))
			m.ui.palSel, m.ui.palMoved = 0, false
		}
		return
	}
	name, rest, _ := strings.Cut(body, " ")
	c := paletteFind(name)
	if c == nil {
		return
	}
	words := strings.Split(rest, " ")
	n := len(words) - 1 // the argument being typed
	if n >= len(c.Args) || c.Args[n].Complete == nil || c.Args[n].Rest {
		return
	}
	for _, cand := range c.Args[n].Complete(m) {
		if strings.HasPrefix(cand, words[n]) {
			words[n] = cand
			line := "/" + name + " " + strings.Join(words, " ") + " "
			m.setLine(line, len(line))
			return
		}
	}
}

// openPalette is Ctrl+P: the line opened with / typed (a draft in the line
// is kept for i, as Esc keeps it).
func (m *Model) openPalette() {
	if m.ui.line == lineMessage {
		m.leaveLine(true)
	}
	m.openLine(lineMessage, "/")
	m.ui.palSel, m.ui.palMoved, m.ui.palHidden = 0, false, false
}

// paletteMax bounds the rows the list shows.
const paletteMax = 10

// paletteLines are the list's rows, best first, read top-down and ending
// right above the line (at most room): the chosen row a pill, each with its
// summary and the key that does the same.
func (m *Model) paletteLines(width, room int) []Line {
	if !m.paletteListOn() || room <= 0 {
		return nil
	}
	rows := m.paletteRows()
	if len(rows) == 0 {
		return []Line{FitLine(Line{S(RoleMuted, " no command matches: / alone lists them")}, width)}
	}
	n := min(len(rows), paletteMax, room)
	sel := min(m.ui.palSel, len(rows)-1)
	first := 0
	if sel >= n {
		first = sel - n + 1
	}
	asks := len(openAsks(m.snap.asks)) > 0
	nameW := 0
	for _, c := range paletteTable {
		nameW = max(nameW, Width(paletteUsage(c)))
	}
	out := make([]Line, 0, n)
	for i := first; i < first+n; i++ {
		c := paletteTable[rows[i]]
		key := paletteKeyHint(c, asks)
		keyW := Width(key) + 2
		l := Line{S(RoleText, " "), Seg{Text: " " + Fit(paletteUsage(c), nameW) + " ", Role: RoleAccent, Pill: i == sel},
			S(RoleText, " "), S(RoleMuted, Fit(c.Summary, max(0, width-nameW-5-keyW)))}
		if key != "" {
			l = append(l, S(RoleText, strings.Repeat(" ", max(1, width-Line(l).Width()-keyW+1))), S(RoleAccent, key))
		}
		out = append(out, FitLine(l, width))
	}
	return out
}

// paletteUsage is a row's name and arguments: "write <agent> <text>".
func paletteUsage(c palCmd) string {
	u := "/" + c.Name
	for _, a := range c.Args {
		if a.Optional {
			u += " [" + a.Name + "]"
		} else {
			u += " <" + a.Name + ">"
		}
	}
	return u
}
