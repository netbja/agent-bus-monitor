package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// A setting's fixed list (language-picker): a key with config.Key.Options
// is chosen from its values, never typed. Enter opens the list under the
// row on the current value; typing filters it with the command palette's
// ranking; ↑↓ (pgup pgdn) move; Enter writes the value under the cursor
// through config.Set, the key's own rule judging it once more; Esc leaves
// the file as it is. A filter that keeps nothing writes nothing. Generic:
// any key that gains Options gets it.

// optionListRows is how many entries the list shows at once.
const optionListRows = 8

// optionList is an open list: every option, the filter typed, and the
// cursor among the options the filter keeps.
type optionList struct {
	opts   []config.Option
	filter string
	sel    int
}

// newOptionList opens on current's entry (the first when it has none).
func newOptionList(opts []config.Option, current string) *optionList {
	l := &optionList{opts: opts}
	for i, o := range opts {
		if o.Value == current {
			l.sel = i
		}
	}
	return l
}

// optionMatch ranks opts for q as the palette ranks its commands: the
// value is the name, the label the summary, searched with and without its
// accents (config.Option.Search; francais finds Français, foureyes).
func optionMatch(opts []config.Option, q string) []int {
	q = config.FoldAccents(q)
	return rankMatches(len(opts), q, func(i int) (string, []string, string) { return opts[i].Value, nil, opts[i].Search() })
}

// visible are the options the filter keeps, best first.
func (l *optionList) visible() []int { return optionMatch(l.opts, l.filter) }

// current is the value under the cursor, "" when the filter keeps nothing.
func (l *optionList) current() string {
	v := l.visible()
	if l.sel < 0 || l.sel >= len(v) {
		return ""
	}
	return l.opts[v[l.sel]].Value
}

// optionListKey is a key while the list is open.
func (m *Model) optionListKey(s *settingsPage, k Key, name, path string) tea.Cmd {
	l := s.list
	n := len(l.visible())
	switch k.Code {
	case keyUp:
		l.sel = max(0, l.sel-1)
	case keyDown:
		l.sel = max(0, min(n-1, l.sel+1))
	case keyPgUp:
		l.sel = max(0, l.sel-optionListRows)
	case keyPgDn:
		l.sel = max(0, min(n-1, l.sel+optionListRows))
	case keyBackspace:
		if r := []rune(l.filter); len(r) > 0 {
			l.filter, l.sel = string(r[:len(r)-1]), 0
		}
	case keyRune:
		l.filter, l.sel = l.filter+string(k.Rune), 0
	case keyEnter:
		v := l.current()
		if v == "" {
			s.err = "nothing matches " + fmt.Sprintf("%q", l.filter) + ": only an entry of the list can be chosen"
			return nil
		}
		s.err = ""
		return m.settingsCmd(func() error { return config.Set(path, name, v) }, name)
	}
	return nil
}

// lines are the list under its row: a window of optionListRows entries
// around the cursor, "value  label" each, the cursor's in the accent; then
// how many match and the keys. Every line fits width.
func (l *optionList) lines(width int) []Line {
	v := l.visible()
	indent := "      "
	var out []Line
	if len(v) == 0 {
		return []Line{FitLine(Line{S(RoleText, indent), S(RoleAttention, fmt.Sprintf("nothing matches %q", l.filter)),
			S(RoleMuted, " "+GlyphNone+" backspace widens it "+GlyphNone+" esc cancels")}, width)}
	}
	top := clamp(l.sel-optionListRows/2, 0, max(0, len(v)-optionListRows))
	for i := top; i < min(len(v), top+optionListRows); i++ {
		o := l.opts[v[i]]
		mark, role := "  ", RoleText
		if i == l.sel {
			mark, role = GlyphSelect+" ", RoleAccent
		}
		out = append(out, FitLine(Line{S(RoleText, indent), S(role, mark), {Text: o.Value, Role: role, Bold: i == l.sel},
			S(role, "  "+Preview(o.Label))}, width))
	}
	out = append(out, FitLine(Line{S(RoleText, indent), S(RoleMuted, fmt.Sprintf("%d of %d %s %s%s choose %s enter sets it %s esc cancels",
		len(v), len(l.opts), GlyphNone, GlyphUp, GlyphDownArrow, GlyphNone, GlyphNone))}, width))
	return out
}
