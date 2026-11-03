package main

import (
	"errors"
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
var settingsPageGroups = []config.Group{config.GroupDisplay, config.GroupAttention, config.GroupIdentity, config.GroupBackup}

// settingsPage is the page's state.
type settingsPage struct {
	groups  []settingGroup
	loaded  bool
	edit    string // the key being edited ("" = none)
	text    string // the editor's text
	choice  int    // a key of settingChoices: the option under the cursor
	confirm string // the key a d waits a y for
	err     string // the last refusal, in the rule's own words
	busy    bool   // a write is in flight: keys wait for its rows
}

// themeChoices are display.theme's values, in the page's order.
var themeChoices = []string{"auto", "dark", "light"}

// settingChoices are the keys chosen with ←→ among their values, in the
// page's order (busmon-icons: display.icons joins the theme).
var settingChoices = map[string][]string{"display.theme": themeChoices, "display.icons": {iconsOff, iconsNerd, iconsFont}}

// settingsMsg is the rows read again, after an open or a write.
type settingsMsg struct {
	groups []settingGroup
	key    string // the key written or unset ("" = an open)
	err    error
}

// rows is the page's rows in order, flat.
func (s *settingsPage) rows() []settingRow {
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
	return m.settingsCmd(func() error { return nil }, "")
}

// settingsCmd runs write (a config.Set, an Unset, or nothing) then reads the
// rows again, in a command.
func (m *Model) settingsCmd(write func() error, key string) tea.Cmd {
	if p := m.settingsPageOpen(); p != nil && key != "" {
		p.set.busy = true
	}
	getenv, flags := m.settingsGetenv(), m.cfg.flags
	return func() tea.Msg {
		err := write()
		return settingsMsg{groups: settingsGroups(getenv, flags, settingsPageGroups), key: key, err: err}
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
	s.groups, s.loaded, s.busy, p.dirty = msg.groups, true, false, true
	var refused *config.RefusedError
	switch {
	case msg.err != nil && errors.As(msg.err, &refused):
		s.err = refused.Why // the editor stays open
	case msg.err != nil:
		s.err, s.edit, s.confirm = msg.err.Error(), "", ""
	case msg.key != "":
		s.err, s.edit, s.confirm = "", "", ""
		m.ui.flash, m.ui.flashErr = m.applySettings(msg.key), false
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

// settingsKey is a key on the settings page.
func (m *Model) settingsKey(k Key) tea.Cmd {
	p := m.rd.top()
	s := p.set
	rows := s.rows()
	if !s.loaded || s.busy || len(rows) == 0 { // a key waits for the rows a write reads again
		if k.Code == keyEscape {
			m.pop()
		}
		return nil
	}
	p.sel = clamp(p.sel, 0, len(rows)-1)
	row := rows[p.sel]
	p.dirty = true
	defer m.refresh()
	if s.confirm != "" { // a d waits its y; anything else keeps the file as it is
		key := s.confirm
		s.confirm = ""
		if k.Code == keyRune && k.Rune == 'y' {
			path := config.Path(m.settingsGetenv())
			return m.settingsCmd(func() error { return config.Unset(path, key) }, key)
		}
		return nil
	}
	if s.edit != "" {
		return m.settingsEditKey(k, row)
	}
	switch {
	case k.Code == keyEscape:
		m.pop()
	case k.Code == keyUp || k.Code == keyRune && k.Rune == 'k':
		p.sel = clamp(p.sel-1, 0, len(rows)-1)
		s.err = ""
	case k.Code == keyDown || k.Code == keyRune && k.Rune == 'j':
		p.sel = clamp(p.sel+1, 0, len(rows)-1)
		s.err = ""
	case k.Code == keyRune && k.Rune == 'd' && row.Editable && row.InFile:
		s.confirm = row.Key
	case (k.Code == keyEnter || k.Code == keyRune && k.Rune == ' ') && row.Editable:
		key, _ := config.Lookup(row.Key)
		switch {
		case key.Kind == config.Bool: // one key, one meaning: written at once
			path := config.Path(m.settingsGetenv())
			v := row.Value != "on"
			return m.settingsCmd(func() error { return config.Set(path, row.Key, v) }, row.Key)
		case settingChoices[row.Key] != nil:
			s.edit, s.choice = row.Key, max(0, indexOf(settingChoices[row.Key], row.Value))
		default:
			s.edit, s.text = row.Key, editText(key, row.Value)
		}
	case k.Code == keyEnter && !row.Editable && row.PersonOnly:
		m.ui.flash, m.ui.flashErr = row.Label+" is your own setting: edit config.toml yourself", true
	case k.Code == keyEnter && !row.Editable:
		m.ui.flash, m.ui.flashErr = row.Label+" is set by "+row.Source+": unset it there for the file to apply", true
	}
	return nil
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

// settingsTitle is the page's counter summary.
func settingsTitle(s *settingsPage) string {
	var here, elsewhere, refused, def int
	for _, r := range s.rows() {
		switch r.Sym {
		case GlyphWorking:
			here++
		case GlyphNone:
			elsewhere++
		case GlyphBlocked:
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

// settingsBody lays the page out in width cells: the groups and their rows,
// the row being changed with its editor, a refusal under it, then the lines
// that say where a change goes and when it applies.
func settingsBody(s *settingsPage, sel, width int) []Line {
	var b body
	if !s.loaded {
		b.seg(RoleMuted, "  reading the settings").text("\n")
		return b.Lines()
	}
	const labelW, valueW = 24, 22
	i := 0
	for _, g := range s.groups {
		b.segs(listSection(string(g.Group))...).text("\n")
		for _, r := range g.Rows {
			mark := planMark(i == sel)
			role := RoleText
			switch r.Sym {
			case GlyphWorking:
				role = RoleAccent
			case GlyphBlocked:
				role = RoleAttention
			case GlyphIdle, GlyphNone:
				role = RoleMuted
			}
			value := r.Value
			if s.edit == r.Key {
				if choices := settingChoices[r.Key]; choices != nil {
					value = GlyphArrowL + " " + choices[s.choice] + " " + GlyphArrowR
				} else {
					value = "[ " + s.text + " ]"
				}
			}
			b.segs(mark...).seg(role, r.Sym).text(" ").text(Fit(Preview(r.Label), labelW)+"  ").
				text(Fit(Preview(value), valueW)+"  ").seg(RoleMuted, Preview(r.Source)).text("\n")
			if r.Key == "display.icons" {
				b.segs(iconSample(width)...).text("\n")
			}
			if i == sel && s.err != "" {
				b.text("      ").seg(RoleAttention, Preview(s.err)).text("\n")
			}
			if i == sel && s.confirm == r.Key {
				b.text("      ").seg(RoleAttention, Preview("unset "+r.Label+" here? "+r.AfterUnset+" will apply (y)")).text("\n")
			}
			i++
		}
	}
	b.text("\n").seg(RoleMuted, "  Changes   theme, icons, fade, bell and reminder timings at once; the others at busmon's next start").text("\n")
	b.seg(RoleMuted, "  Written   config.toml only; a value from the environment or a flag is read-only here").text("\n")
	lines := b.Lines()
	for j := range lines {
		lines[j] = FitLine(lines[j], width)
	}
	return lines
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
