package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// The settings page's interaction (spec docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md,
// with the person's answers: busmon writes config.toml; Display, Attention
// and Identity first; busmon's display keys apply at once; key C). Its rows
// are read and written in commands, never in View.

// settingsPageGroups are the groups the page shows (the person's answer:
// these first, the services' thresholds staying in the file for now), and
// Backup for its person-only keys alone, read-only (backup spec: shown, never
// written by busmon; settingOnPage).
var settingsPageGroups = []config.Group{config.GroupDisplay, config.GroupAttention, config.GroupIdentity, config.GroupNotifications, config.GroupBackup}

// settingsPage is the page's state (settings-panels: the groups on the left,
// the selected group's keys on the right; page.sel is the row in that group).
type settingsPage struct {
	groups  []settingGroup
	group   int      // the selected group
	keys    bool     // the focus is on the keys (else on the groups: settings-focus)
	targets []string // notify.toml's targets under the Notifications rows
	loaded  bool
	edit    string   // the key being edited ("" = none)
	text    string   // the editor's text
	choice  int      // a key of settingChoices: the option under the cursor
	confirm string   // the key a d waits a y for
	reset   []string // the keys a D waits a y for (nil = none)
	err     string   // the last refusal, in the rule's own words
	busy    bool     // a write is in flight: keys wait for its rows
	selLine int      // the body line of the selected row, to keep it in view
}

// themeChoices are display.theme's values, in the page's order.
var themeChoices = []string{"auto", "dark", "light"}

// settingChoices are the keys chosen with ←→ among their values, in the
// page's order (busmon-icons: display.icons joins the theme).
var settingChoices = map[string][]string{"display.theme": themeChoices, "display.icons": {iconsOff, iconsNerd, iconsFont}}

// settingsMsg is the rows read again, after an open or a write.
type settingsMsg struct {
	groups  []settingGroup
	targets []string // notify.toml's targets, one line each, never a URL
	keys    []string // the keys written or unset (none = an open)
	err     error
}

// rows is the selected group's rows.
func (s *settingsPage) rows() []settingRow {
	if s.group < 0 || s.group >= len(s.groups) {
		return nil
	}
	return s.groups[s.group].Rows
}

// allRows is every group's rows in order, flat (the title's counts).
func (s *settingsPage) allRows() []settingRow {
	var out []settingRow
	for _, g := range s.groups {
		out = append(out, g.Rows...)
	}
	return out
}

func (m *Model) settingsGetenv() func(string) string {
	if m.cfg.getenv != nil {
		return m.cfg.getenv
	}
	return func(string) string { return "" } // a test reads no file
}

// openSettings opens the page and reads its rows.
func (m *Model) openSettings() tea.Cmd {
	m.push(page{kind: ovSettings, set: &settingsPage{}})
	return m.settingsCmd(func() error { return nil })
}

// settingsCmd runs write (a config.Set, Unsets, or nothing) for keys then
// reads the rows again once, in a command.
func (m *Model) settingsCmd(write func() error, keys ...string) tea.Cmd {
	if p := m.settingsPageOpen(); p != nil && len(keys) > 0 {
		p.set.busy = true
	}
	getenv, flags := m.settingsGetenv(), m.cfg.flags
	return func() tea.Msg {
		err := write()
		return settingsMsg{groups: settingsGroups(getenv, flags, settingsPageGroups), targets: notifyTargetLines(getenv), keys: keys, err: err}
	}
}

func (m *Model) settingsPageOpen() *page {
	for i := len(m.rd.pages) - 1; i >= 0; i-- {
		if m.rd.pages[i].kind == ovSettings {
			return &m.rd.pages[i]
		}
	}
	return nil
}

// gotSettings takes the rows read; after a write, busmon's own settings that
// apply at once are applied (the person's answer), and the flash says when
// the others do.
func (m *Model) gotSettings(msg settingsMsg) {
	p := m.settingsPageOpen()
	if p == nil {
		return
	}
	s := p.set
	s.groups, s.targets, s.loaded, s.busy, p.dirty = msg.groups, msg.targets, true, false, true
	var refused *config.RefusedError
	switch {
	case msg.err != nil && errors.As(msg.err, &refused):
		s.err = refused.Why // the editor stays open
	case msg.err != nil:
		s.err, s.edit, s.confirm, s.reset = msg.err.Error(), "", "", nil
	case len(msg.keys) > 0:
		s.err, s.edit, s.confirm, s.reset = "", "", "", nil
		var said []string
		for _, k := range msg.keys {
			if w := m.applySettings(k); indexOf(said, w) < 0 {
				said = append(said, w)
			}
		}
		m.ui.flash, m.ui.flashErr = strings.Join(said, "; "), false
		if len(msg.keys) > 1 {
			m.ui.flash = fmt.Sprintf("%d keys reset: %s", len(msg.keys), m.ui.flash)
		}
	}
	m.refresh()
}

// applySettings applies busmon's own settings read again (bell, fade, the
// reminder and recap timings, the theme) and says when key applies.
func (m *Model) applySettings(key string) string {
	ns := loadBusmonSettings(m.settingsGetenv(), m.cfg.flags)
	cur := &m.cfg.settings
	cur.Bell, cur.Fade, cur.PopupAfter, cur.Snooze, cur.AutoAfter = ns.Bell, ns.Fade, ns.PopupAfter, ns.Snooze, ns.AutoAfter
	switch key {
	case "display.theme":
		if m.cfg.retheme == nil || ns.Theme == cur.Theme {
			return "theme saved: next start of busmon"
		}
		t, err := m.cfg.retheme(ns.Theme)
		if err != nil {
			return "theme saved: next start of busmon"
		}
		th, cur.Theme, m.paint = t, ns.Theme, newPainter(t)
		return "theme applied and saved"
	case "display.icons": // busmon-icons: drawn from the next frame on
		cur.Icons = ns.Icons
		return "icons applied and saved"
	case "waiting.bell", "waiting.fade", "waiting.popup_after", "waiting.snooze", "recap.auto_after":
		return "applied and saved"
	}
	return "saved: next start of busmon"
}

// settingsKey is a key on the settings page (ModeSettings): a pending y
// first, then the editor, which takes every key while open (so ←→ change a
// choice and a typed q is text), then the page's table.
func (m *Model) settingsKey(act Action, k Key) tea.Cmd {
	p := m.rd.top()
	s := p.set
	if !s.loaded || s.busy || len(s.groups) == 0 { // a key waits for the rows a write reads again
		if k.Code == keyEscape {
			m.pop()
		}
		return nil
	}
	s.group = clamp(s.group, 0, len(s.groups)-1)
	rows := s.rows()
	p.sel = clamp(p.sel, 0, max(0, len(rows)-1))
	p.dirty = true
	defer m.keepSettingInView(p)
	path := config.Path(m.settingsGetenv())
	if s.confirm != "" || s.reset != nil { // a d or D waits its y; anything else keeps the file as it is
		key, keys := s.confirm, s.reset
		s.confirm, s.reset = "", nil
		if k.Code != keyRune || k.Rune != 'y' {
			return nil
		}
		if key != "" {
			return m.settingsCmd(func() error { return config.Unset(path, key) }, key)
		}
		return m.settingsCmd(func() error {
			for _, key := range keys {
				if err := config.Unset(path, key); err != nil {
					return err
				}
			}
			return nil
		}, keys...)
	}
	if s.edit != "" && len(rows) > 0 {
		return m.settingsEditKey(k, rows[p.sel])
	}
	side := m.readerWidth() >= settingsSideMin
	step := func(d int) {
		n := len(s.groups)
		if side {
			s.group = clamp(s.group+d, 0, n-1)
		} else {
			s.group = (s.group + d + n) % n
		}
		p.sel, p.top, s.err = 0, 0, ""
	}
	switch act {
	case ActClose:
		m.pop()
		return nil
	case ActFocusToggle:
		s.keys, s.err = !s.keys, ""
		return nil
	case ActUp, ActDown:
		d := 1
		if act == ActUp {
			d = -1
		}
		switch {
		case !s.keys && side:
			step(d)
		case !s.keys && act == ActDown: // the tab bar: ↓ into its keys
			s.keys = true
		case !s.keys:
		case act == ActUp && p.sel == 0 && !side: // ↑ on the first key: back to the tabs
			s.keys = false
		default:
			p.sel, s.err = clamp(p.sel+d, 0, max(0, len(rows)-1)), ""
		}
		return nil
	case ActGroupNext, ActGroupPrev:
		switch {
		case s.keys && act == ActGroupPrev:
			s.keys, s.err = false, ""
		case s.keys:
		case side && act == ActGroupNext:
			s.keys = true
		case side:
		case act == ActGroupNext:
			step(1)
		default:
			step(-1)
		}
		return nil
	case ActResetGroup:
		keys, kept := settingsResettable(s.groups[s.group])
		if len(keys) == 0 {
			m.ui.flash, m.ui.flashErr = settingsNothingToReset(s.groups[s.group], kept), true
			return nil
		}
		s.reset = keys
		return nil
	}
	if !s.keys { // enter on a group goes to its keys; d waits for a key
		if act == ActOpen {
			s.keys = true
		}
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	row := rows[p.sel]
	switch {
	case act == ActReset && row.Editable && row.InFile:
		s.confirm = row.Key
	case act == ActOpen && row.Editable:
		key, _ := config.Lookup(row.Key)
		switch {
		case key.Kind == config.Bool: // one key, one meaning: written at once
			v := row.Value != "on"
			return m.settingsCmd(func() error { return config.Set(path, row.Key, v) }, row.Key)
		case settingChoices[row.Key] != nil:
			s.edit, s.choice = row.Key, max(0, indexOf(settingChoices[row.Key], row.Value))
		default:
			s.edit, s.text = row.Key, editText(key, row.Value)
		}
	case act == ActOpen && row.PersonOnly:
		m.ui.flash, m.ui.flashErr = row.Label+" is your own setting: edit config.toml yourself", true
	case act == ActOpen:
		m.ui.flash, m.ui.flashErr = row.Label+" is set by "+row.Source+": unset it there for the file to apply", true
	}
	return nil
}

// settingsResettable is what D removes from config.toml in g: every key the
// file holds a line for (a refused line, one a flag or the env shadows), in
// order; kept
// names the person's own keys the file holds, which stay (backup spec).
func settingsResettable(g settingGroup) (keys, kept []string) {
	for _, r := range g.Rows {
		switch {
		case !r.FileHeld:
		case r.PersonOnly:
			kept = append(kept, r.Key)
		default: // shadowed by a flag or the env, its line is still the file's
			keys = append(keys, r.Key)
		}
	}
	return keys, kept
}

// settingsNothingToReset is the flash of a D with nothing to remove.
func settingsNothingToReset(g settingGroup, kept []string) string {
	if len(kept) > 0 {
		return string(g.Group) + ": nothing to reset; your own " + settingShortKeys(kept) + " stay: edit config.toml yourself"
	}
	return string(g.Group) + ": nothing set in config.toml"
}

// settingShortKeys is keys without their table, comma-separated.
func settingShortKeys(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		_, out[i], _ = strings.Cut(k, ".")
	}
	return strings.Join(out, ", ")
}

// keepSettingInView rebuilds the page and scrolls it so the selected row
// shows.
func (m *Model) keepSettingInView(p *page) {
	m.refresh()
	if p.set == nil {
		return
	}
	if rows := m.readerRows(); rows > 0 && (p.set.selLine < p.top || p.set.selLine >= p.top+rows) {
		p.top = max(0, p.set.selLine-3)
		m.clampPage(p)
	}
}

// settingsEditKey is a key while a row is being changed: the theme's choice
// (←→), or the line editor; Enter writes through the key's own rule, Esc
// leaves the file untouched.
func (m *Model) settingsEditKey(k Key, row settingRow) tea.Cmd {
	s := m.rd.top().set
	path := config.Path(m.settingsGetenv())
	if k.Code == keyEscape {
		s.edit, s.err = "", ""
		return nil
	}
	if choices := settingChoices[s.edit]; choices != nil {
		switch k.Code {
		case keyLeft:
			s.choice = (s.choice + len(choices) - 1) % len(choices)
		case keyRight:
			s.choice = (s.choice + 1) % len(choices)
		case keyEnter:
			v := choices[s.choice]
			return m.settingsCmd(func() error { return config.Set(path, row.Key, v) }, row.Key)
		}
		return nil
	}
	switch {
	case k.Code == keyBackspace:
		if r := []rune(s.text); len(r) > 0 {
			s.text = string(r[:len(r)-1])
		}
	case k.Code == keyRune:
		s.text += string(k.Rune)
	case k.Code == keyEnter:
		key, _ := config.Lookup(s.edit)
		v, why := parseSetting(key, s.text)
		if why != "" {
			s.err = why
			return nil
		}
		name := s.edit
		return m.settingsCmd(func() error { return config.Set(path, name, v) }, name)
	}
	return nil
}

// editText is a value as the editor starts with it: the file's own form.
func editText(k *config.Key, shown string) string {
	switch {
	case k.Name == "display.limit" && shown == "all":
		return "0"
	case shown == "off" && k.Kind == config.Duration:
		return "0"
	case shown == "none":
		return ""
	}
	return shown
}

// parseSetting reads the editor's text as k's kind; the key's own rule then
// judges it in config.Set. display.limit's every value at or below 0 reads
// all, and the page writes 0 for it (spec).
func parseSetting(k *config.Key, text string) (any, string) {
	text = strings.TrimSpace(text)
	if k.List { // typed comma-separated, written as a TOML list
		items := []string{}
		for _, s := range strings.Split(text, ",") {
			if s = strings.TrimSpace(s); s != "" {
				items = append(items, s)
			}
		}
		return items, ""
	}
	switch k.Kind {
	case config.Int:
		if k.Name == "display.limit" && text == "all" {
			return 0, ""
		}
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, "want a whole number"
		}
		if k.Name == "display.limit" && n < 0 {
			n = 0
		}
		return n, ""
	case config.Float:
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, "want a number"
		}
		return f, ""
	case config.Duration:
		if text == "off" || text == "0" {
			return time.Duration(0), ""
		}
		d, err := time.ParseDuration(text)
		if err != nil {
			return nil, "want a duration, such as \"30m\""
		}
		return d, ""
	}
	return text, ""
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// settingsTitle is the page's counter summary, over every group.
func settingsTitle(s *settingsPage) string {
	var here, elsewhere, refused, def int
	for _, r := range s.allRows() {
		switch r.From {
		case fromFile:
			here++
		case fromElsewhere:
			elsewhere++
		case fromRefused:
			refused++
		default:
			def++
		}
	}
	sum := listSummary(listCount{here, "set here"}, listCount{elsewhere, "elsewhere"}, listCount{refused, "refused"}, listCount{def, "default"})
	if sum == "" {
		return "Settings"
	}
	return "Settings " + GlyphNone + " " + sum
}

// settingsSideW is the groups' sidebar; at a page narrower than
// settingsSideMin (a terminal under 96 cells) the groups are a tab bar above
// the keys instead.
const (
	settingsSideW   = 18
	settingsSideMin = 92
	settingsLabelW  = 26
	settingsValueW  = 28 // a longer value is cut, with its ellipsis
)

// settingsGroupNotes are a group's line beside its name.
var settingsGroupNotes = map[config.Group]string{
	config.GroupDisplay:       "how busmon looks; theme, icons and fade apply at once",
	config.GroupAttention:     "how busmon calls you; bell and reminder timings apply at once",
	config.GroupIdentity:      "your name as a person on the bus",
	config.GroupNotifications: "what reaches your phone or desktop; targets in notify.toml",
	config.GroupBackup:        "your own command after each backup: edit config.toml yourself",
}

// settingsBody lays the page out in width cells: the groups on the left (or
// a tab bar above, on a narrow page), the selected group's keys on the right,
// each value drawn as a control, then the selected key's detail zone.
func settingsBody(s *settingsPage, sel, width int) []Line {
	var b body
	if !s.loaded {
		b.seg(RoleMuted, "  reading the settings").text("\n")
		return b.Lines()
	}
	if len(s.groups) == 0 {
		return nil
	}
	s.group = clamp(s.group, 0, len(s.groups)-1)
	side := width >= settingsSideMin
	panelW := width
	if side {
		panelW = width - settingsSideW - 1
	}
	right, selLine := settingsPanel(s, sel, panelW)
	var lines []Line
	if side {
		for i := 0; i < max(len(right), len(s.groups)); i++ {
			l := Line{}
			if i < len(s.groups) {
				l = append(l, settingsSideRow(s.groups[i].Group, i == s.group, !s.keys)...)
			}
			l = FitLine(l, settingsSideW)
			l = append(l, S(RoleText, strings.Repeat(" ", settingsSideW-l.Width())), S(RoleLine, glyphVertical))
			if i < len(right) {
				l = append(l, right[i]...)
			}
			lines = append(lines, FitLine(l, width))
		}
	} else {
		lines = append(lines, FitLine(settingsTabs(s.groups, s.group, width, !s.keys), width), Line{})
		selLine += 2
		for _, l := range right {
			lines = append(lines, FitLine(l, width))
		}
	}
	s.selLine = selLine
	return lines
}

// settingsSideRow is a group in the sidebar: the selected one marked, in the
// accent and bold while the groups have the focus, plain text when the keys
// have it (still seen, no longer lit); the others muted.
func settingsSideRow(g config.Group, selected, focused bool) Line {
	switch {
	case selected && focused:
		return Line{S(RoleAccent, " "+GlyphPrompt+" "), {Text: string(g), Role: RoleAccent, Bold: true}}
	case selected:
		return Line{S(RoleMuted, " "+GlyphPrompt+" "), S(RoleText, string(g))}
	}
	return Line{S(RoleMuted, "   "+string(g))}
}

// settingsTabs is the groups as the lower panel's tab bar draws its tabs
// (tabBar's blocks): the selected one a pill, dimmed with the frame's fade
// while the keys have the focus, as an unfocused panel's tab is; the others
// grey; on a page too narrow for them all, those farthest from the selected
// one give way to an ellipsis on their side.
func settingsTabs(groups []settingGroup, active, width int, focused bool) Line {
	block := func(i int) Seg {
		if i == active {
			fade := 0
			if !focused {
				fade = frameFade
			}
			return Seg{Text: " " + string(groups[i].Group) + " ", Role: RoleAccent, Bold: true, Pill: true, Fade: fade, Tab: true}
		}
		return Seg{Text: " " + string(groups[i].Group) + " ", Role: RoleMuted, Off: true, Tab: true}
	}
	w := func(i int) int { return Width(block(i).Text) + 1 }
	lo, hi, used := active, active, w(active)
	for grew := true; grew; {
		grew = false
		mark := func(lo, hi int) int { // the ellipses the window still needs
			n := 0
			if lo > 0 {
				n += 2
			}
			if hi < len(groups)-1 {
				n += 2
			}
			return n
		}
		if hi+1 < len(groups) && used+w(hi+1)+mark(lo, hi+1) <= width {
			hi, used, grew = hi+1, used+w(hi+1), true
		}
		if lo > 0 && used+w(lo-1)+mark(lo-1, hi) <= width {
			lo, used, grew = lo-1, used+w(lo-1), true
		}
	}
	var l Line
	if lo > 0 {
		l = append(l, S(RoleMuted, GlyphEllipsis+" "))
	}
	for i := lo; i <= hi; i++ {
		l = append(l, block(i), Seg{Text: " ", Role: RoleText, Tab: true})
	}
	if hi < len(groups)-1 {
		l = append(l, S(RoleMuted, GlyphEllipsis+" "))
	}
	return l
}

// settingsPanel is the selected group's side of the page in width cells: its
// name and note, its keys as controls, then the detail zone of the selected
// one; selLine is that row's line.
func settingsPanel(s *settingsPage, sel, width int) ([]Line, int) {
	g := s.groups[s.group]
	var b body
	b.text(" ").segs(Seg{Text: string(g.Group), Role: RoleText, Bold: true}).
		seg(RoleMuted, "  "+settingsGroupNotes[g.Group]).text("\n\n")
	n, selLine := 2, 2
	labelW := settingsLabelCol(g)
	for i, r := range g.Rows {
		if i == sel {
			selLine = n
		}
		b.segs(settingsKeyRow(s, r, i == sel, labelW, width)...).text("\n")
		n++
		if r.Key == "display.icons" {
			b.segs(iconSample(width)...).text("\n")
			n++
		}
	}
	if g.Group == config.GroupNotifications {
		for _, l := range s.targets {
			b.seg(RoleMuted, "    "+Preview(l)).text("\n")
		}
	}
	if len(g.Rows) > 0 {
		r := g.Rows[clamp(sel, 0, len(g.Rows)-1)]
		b.text("\n").text(" ").seg(RoleLine, strings.Repeat(glyphHorizontal, max(0, width-2))).text("\n")
		note := func(text string, role Role) {
			b.join(wrapSpans([]mdSpan{{text: Preview(text), role: role}}, width-1, " ", " ")).text("\n")
		}
		b.join(wrapSpans(settingsDetail(r), width-1, " ", " ")).text("\n")
		if h := settingHelp(r); h != "" {
			note(h, RoleMuted)
		}
		switch {
		case s.err != "":
			note(s.err, RoleAttention)
		case s.confirm == r.Key:
			note("unset "+r.Label+" here? "+r.AfterUnset+" will apply (y)", RoleAttention)
		}
		if s.reset != nil {
			note(settingsResetQuestion(g, s.reset), RoleAttention)
		}
	}
	return b.Lines(), selLine
}

// settingsResetQuestion is D's confirmation: the keys config.toml loses, the
// ones a flag or the env still sets after it (master), and the person's own
// keys that stay.
func settingsResetQuestion(g settingGroup, keys []string) string {
	q := fmt.Sprintf("Reset %s? %d key", g.Group, len(keys))
	if len(keys) > 1 {
		q += "s"
	}
	q += " set in config.toml (" + settingShortKeys(keys) + ") will be removed"
	for _, r := range g.Rows { // a flag or the env still wins once its line is gone
		if r.FileHeld && !r.PersonOnly && (strings.HasPrefix(r.Source, "flag ") || strings.HasPrefix(r.Source, "env ")) {
			q += "; " + settingShortKeys([]string{r.Key}) + " still follows " + r.Source
		}
	}
	if _, kept := settingsResettable(g); len(kept) > 0 {
		q += "; your own " + settingShortKeys(kept) + " stay"
	}
	return q + " (y)"
}

// settingsLabelCol is the group's label column: its longest label, at most
// settingsLabelW, so the values start in one column after it
// (busmon-detail-labels' rule, settings-focus).
func settingsLabelCol(g settingGroup) int {
	w := 0
	for _, r := range g.Rows {
		w = max(w, Width(Preview(r.Label)))
	}
	return min(w, settingsLabelW)
}

// settingsGutter is the cells between the label column and the values.
const settingsGutter = 3

// settingsKeyRow is one key: the selection mark (the accent while the keys
// have the focus, muted when the groups have it), • when config.toml sets it
// to other than its default, its label in the group's label column, then its
// value as a control after the gutter.
func settingsKeyRow(s *settingsPage, r settingRow, selected bool, labelW, width int) Line {
	l := Line{S(RoleText, " ")}
	switch {
	case selected && s.keys:
		l = append(l, planMark(true)...)
	case selected:
		l = append(l, S(RoleMuted, GlyphSelect), S(RoleText, " "))
	default:
		l = append(l, planMark(false)...)
	}
	if r.Set {
		l = append(l, S(RoleAccent, GlyphSet+" "))
	} else {
		l = append(l, S(RoleText, "  "))
	}
	l = append(l, S(RoleText, Fit(Preview(r.Label), labelW)), S(RoleText, strings.Repeat(" ", settingsGutter)))
	return FitLine(append(l, settingControl(s, r)...), width)
}

// settingControl is a row's value as a control: on/off a dot (on in the ok
// colour), a choice ‹ value ›, the rest plain; the value being edited its
// editor; a value from a flag, the env or busmon.toml muted, a refused one in
// the attention colour.
func settingControl(s *settingsPage, r settingRow) Line {
	if s.edit == r.Key {
		if choices := settingChoices[r.Key]; choices != nil {
			return Line{S(RoleMuted, GlyphArrowL+" "), {Text: choices[s.choice], Role: RoleAccent, Bold: true}, S(RoleMuted, " "+GlyphArrowR)}
		}
		return Line{S(RoleAccent, "[ "+Preview(s.text)+" ]")}
	}
	text, role := Preview(r.Value), RoleText
	if Width(text) > settingsValueW {
		text = Fit(text, settingsValueW)
	}
	var l Line
	switch {
	case r.Kind == config.Bool && r.Value == "on":
		l = Line{S(RoleOK, GlyphWorking+" on")}
	case r.Kind == config.Bool:
		l = Line{S(RoleMuted, GlyphIdle+" off")}
	case settingChoices[r.Key] != nil:
		l = Line{S(RoleMuted, GlyphChoiceL+" "), S(RoleAccent, text), S(RoleMuted, " "+GlyphChoiceR)}
	default:
		l = Line{S(role, text)}
	}
	switch r.From {
	case fromElsewhere:
		for i := range l {
			l[i].Role = RoleMuted
		}
	case fromRefused:
		for i := range l {
			l[i].Role = RoleAttention
		}
	}
	return l
}

// settingsDetail is the selected key's line: its label, file key, source in
// agentbus config's words, default and who reads it (wrapped by the page).
func settingsDetail(r settingRow) []mdSpan {
	from := r.Source
	if from == "default" {
		from = "the default"
	}
	spans := []mdSpan{{text: Preview(r.Label) + " " + GlyphNone + " ", role: RoleMuted}, {text: r.Key, role: RoleText},
		{text: " " + GlyphNone + " from ", role: RoleMuted}, {text: Preview(from), role: RoleAccent}}
	if r.From != fromDefault {
		spans = append(spans, mdSpan{text: " (default: " + Preview(r.Default) + ")", role: RoleMuted})
	}
	if r.ReadBy != "" {
		spans = append(spans, mdSpan{text: " " + GlyphNone + " read by " + Preview(r.ReadBy), role: RoleMuted})
	}
	return spans
}

// settingHelps are the page's keys' help lines; a key without one is helped
// by its kind (settingHelp).
var settingHelps = map[string]string{
	"display.theme":           "auto follows the terminal's background; dark or light forces one",
	"display.icons":           "off: none " + GlyphNone + " nerd: a lettered box per provider " + GlyphNone + " agent-font: the maker's logo",
	"display.limit":           "Activity lines replayed when busmon starts; 0 or below: all history",
	"display.idle_hint_after": "flags idle? on a working agent whose pane looks ready this long; 0 is off",
	"waiting.fade":            "fades a waiting agent in and out and slides the gauges (true colour only)",
	"waiting.popup_after":     "the reminder opens once a decision has waited this long",
	"waiting.snooze":          "how long a dismissed reminder stays away",
	"waiting.bell":            "rings the terminal's bell on a new decision for you",
	"recap.auto_after":        "the recap opens by itself after you were away this long",
	"updates.check":           "asks GitHub once a day whether a newer release exists",
	"identity.as":             "your name on the bus, as a person",
	"start.team":              "the roles agentbus start opens for a new project, separated by commas",
	"notify.on":               "sends notifications to the targets of notify.toml",
	"notify.targets":          "the targets of notify.toml to send to, separated by commas",
	"notify.events":           "decision, stall, fail, blocked, prompt, for-you; separated by commas",
	"notify.detail":           "summary sends no agent text; full sends it to a private target only",
	"notify.delay":            "how long an event waits before it is sent",
	"notify.max_per_hour":     "messages an hour at most; a fail or a stall always goes",
	"notify.quiet":            "HH:MM-HH:MM; only the exempt events are sent then",
	"notify.quiet_exempt":     "the events still sent in quiet hours, separated by commas",
}

// settingHelp is r's help line: its own, else what its kind takes.
func settingHelp(r settingRow) string {
	if r.PersonOnly {
		return "your own setting: edit config.toml yourself"
	}
	if h, ok := settingHelps[r.Key]; ok {
		return h
	}
	switch {
	case settingChoices[r.Key] != nil:
		return strings.Join(settingChoices[r.Key], ", ")
	case r.List:
		return "names separated by commas"
	case r.Kind == config.Bool:
		return "on or off"
	case r.Kind == config.Duration:
		return "a duration, such as 30m or 2h"
	case r.Kind == config.Int, r.Kind == config.Float:
		return "a number"
	}
	return ""
}

// iconSampleProviders are the sample's providers, in the spec's order.
var iconSampleProviders = []string{"anthropic", "openai", "moonshot", "xai", "google"}

// iconSample is the Icons row's help line (busmon-icons §6) in width cells:
// each mode's glyphs drawn in that mode, each with its blank cell, so the
// person sees whether the terminal draws them; boxes or tofu there say keep
// off. Fitted by whole pieces, in order, up to the first that does not fit:
// an icon is never cut from its blank nor left against the frame (foureyes).
func iconSample(width int) Line {
	pieces := []Seg{{Text: "    off " + GlyphNone + " nerd ", Role: RoleMuted}}
	for _, mode := range []string{iconsNerd, iconsFont} {
		if mode == iconsFont {
			pieces = append(pieces, Seg{Text: GlyphNone + " agent-font ", Role: RoleMuted})
		}
		for _, p := range iconSampleProviders {
			pieces = append(pieces, iconPair(providerIcon(mode, p), p))
		}
	}
	pieces = append(pieces, Seg{Text: "   (what your terminal draws is what you get)", Role: RoleMuted})
	var l Line
	for _, p := range pieces {
		if l.Width()+Width(p.Text) > width {
			break
		}
		l = append(l, p)
	}
	return l
}
