package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// language-picker: a key with a fixed list (config.Key.Options) is chosen
// from that list, filtered as typed with the command palette's ranking;
// only a list entry can be written.

func languageOptions(t *testing.T) []config.Option {
	t.Helper()
	k, _ := config.Lookup("person.language")
	return k.Choices()
}

// The palette's ranking finds fr by its code, the start of its own name
// and its English name.
func TestOptionMatchFindsALanguage(t *testing.T) {
	opts := languageOptions(t)
	for _, q := range []string{"fr", "FR", "fran", "Français", "french", "Fre", "francais"} { // without its accent too (foureyes)
		got := optionMatch(opts, q)
		if len(got) == 0 || opts[got[0]].Value != "fr" {
			first := ""
			if len(got) > 0 {
				first = opts[got[0]].Value
			}
			t.Errorf("%q: first %q of %d", q, first, len(got))
		}
	}
	if got := optionMatch(opts, "zzq9"); len(got) != 0 {
		t.Errorf("zzq9 matched %d", len(got))
	}
	if got := optionMatch(opts, ""); len(got) != len(opts) {
		t.Errorf("empty: %d of %d", len(got), len(opts))
	}
}

// Enter on Your language opens the list on the current value; typing
// filters it; Enter writes the entry under the cursor; a filter that keeps
// nothing writes nothing; Esc leaves the file as it is.
func TestSettingsLanguageList(t *testing.T) {
	getenv := settingsEnv(t, "[person]\nlanguage = \"de\"\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = press(t, selectSetting(t, m, "person.language"), tea.Key{Code: tea.KeyEnter})
	s := m.rd.top().set
	if s.list == nil || s.edit != "person.language" {
		t.Fatalf("Enter opened no list: edit %q", s.edit)
	}
	if v := s.list.current(); v != "de" {
		t.Fatalf("the cursor is on %q, want the current de", v)
	}
	body := tokLinesText(m.rd.top().body)
	if !strings.Contains(body, "de  Deutsch (German)") {
		t.Fatalf("the list does not show de:\n%s", body)
	}

	for _, r := range "fran" {
		m = press(t, m, r)
	}
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "fr  Français (French)") || strings.Contains(body, "Deutsch") {
		t.Fatalf("filtered list:\n%s", body)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "language = \"fr\"") || m.rd.top().set.list != nil || m.rd.top().set.edit != "" {
		t.Fatalf("fr not written: %q", configText(t, getenv))
	}

	before := configText(t, getenv)
	m = press(t, selectSetting(t, m, "person.language"), tea.Key{Code: tea.KeyEnter})
	for _, r := range "frnvv" {
		m = press(t, m, r)
	}
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "nothing matches") {
		t.Fatalf("no match not said:\n%s", body)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	// The list refuses it itself, before any write: a key whose own rule
	// took any text would still get only an entry of its list.
	if configText(t, getenv) != before || m.rd.top().set.list == nil || !strings.Contains(m.rd.top().set.err, "only an entry of the list") {
		t.Fatalf("frnvv: %q, err %q", configText(t, getenv), m.rd.top().set.err)
	}
	for range 5 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	// Down stops at the last entry (foureyes): "fr" keeps fr and fy.
	for _, r := range "fr" {
		m = press(t, m, r)
	}
	for range 5 {
		m = press(t, m, tea.Key{Code: tea.KeyDown})
	}
	if v := m.rd.top().set.list.current(); v != "fy" {
		t.Fatalf("down past the end: %q, want the last entry fy", v)
	}
	for range 2 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	if v := m.rd.top().set.list.current(); v == "" || v == "fr" {
		t.Fatalf("down moved to %q", v)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})
	if configText(t, getenv) != before || m.rd.top().set.list != nil || m.rd.top().set.edit != "" {
		t.Fatalf("Esc: %q", configText(t, getenv))
	}
}
