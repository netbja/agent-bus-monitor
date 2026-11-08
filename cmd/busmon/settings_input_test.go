package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// The person (settings-duration-input): a number or duration field takes
// only what can form its value; letters and other keys are not typed.
func TestSettingFieldTakesOnlyItsValue(t *testing.T) {
	junk := "----dfasdfad10e"
	for _, k := range config.Keys {
		if k.Kind != config.Duration && k.Kind != config.Int && k.Kind != config.Float {
			continue
		}
		text := ""
		for _, r := range junk {
			text, _ = settingFieldRune(k, text, r)
		}
		allowed := "0123456789"
		switch k.Kind {
		case config.Duration:
			allowed += ".hms"
			if k.Valid("7d") {
				allowed += "d"
			}
		case config.Float:
			allowed += "."
		}
		for i, r := range text {
			if !strings.ContainsRune(allowed, r) && !(r == '-' && i == 0 && settingNegative(k)) {
				t.Errorf("%s: %q typed from %q", k.Name, text, junk)
				break
			}
		}
		if !strings.Contains(text, "10") {
			t.Errorf("%s: the digits were lost: %q", k.Name, text)
		}
	}
	if s, _ := settingFieldRune(mustKey(t, "display.idle_hint_after"), "7", 'd'); s != "7" {
		t.Errorf("a day unit typed where the rule takes none: %q", s)
	}
	if s, _ := settingFieldRune(mustKey(t, "models.source_max_age"), "7", 'd'); s != "7d" {
		t.Errorf("source_max_age takes days: %q", s)
	}
	if s, _ := settingFieldRune(mustKey(t, "display.limit"), "", '-'); s != "-" {
		t.Errorf("display.limit takes a negative: %q", s)
	}
	h := mustKey(t, "display.idle_hint_after")
	for _, c := range [][3]string{{"", "m", ""}, {"10", "m", "10m"}, {"10m", "s", "10ms"}, {"10m", "m", "10m"}, {"1.", ".", "1."}, {"", ".", ""}, {"1.5", ".", "1.5"}, {"1h3", ".", "1h3."}} {
		if got, _ := settingFieldRune(h, c[0], []rune(c[1])[0]); got != c[2] {
			t.Errorf("%q + %q: %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if s, _ := settingFieldRune(mustKey(t, "display.theme"), "da", 'r'); s != "dar" {
		t.Errorf("a text field lost a letter: %q", s)
	}
}

func mustKey(t *testing.T, name string) *config.Key {
	t.Helper()
	k, ok := config.Lookup(name)
	if !ok {
		t.Fatalf("no key %s", name)
	}
	return k
}

// + and - step a number or a duration by its own unit, never below 0 where
// the key takes no negative.
func TestSettingFieldSteps(t *testing.T) {
	for _, c := range []struct {
		key, text string
		r         rune
		want      string
	}{
		{"display.idle_hint_after", "10m", '+', "11m"},
		{"display.idle_hint_after", "10m", '-', "9m"},
		{"display.idle_hint_after", "0", '+', "1m"},
		{"display.idle_hint_after", "0", '-', "0"},
		{"display.idle_hint_after", "90s", '+', "91s"},
		{"display.idle_hint_after", "2h", '-', "1h"},
		{"models.source_max_age", "7d", '+', "8d"},
		{"backup.keep", "3", '+', "4"},
		{"backup.keep", "0", '-', "0"},
		{"display.limit", "5", '-', "4"},
		{"display.limit", "0", '-', "-1"},
		{"dispatch.context_alert", "80", '+', "81"},
		{"display.idle_hint_after", "soon", '+', "soon"}, // an unreadable value is left as it is
	} {
		if got, stepped := settingFieldRune(mustKey(t, c.key), c.text, c.r); got != c.want || (got != c.text && !stepped) {
			t.Errorf("%s %q %c: %q, want %q", c.key, c.text, c.r, got, c.want)
		}
	}
}

// On the page: letters typed into Idle? hint after never reach the field.
func TestSettingsPageIgnoresLetters(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = press(t, selectSetting(t, m, "display.idle_hint_after"), tea.Key{Code: tea.KeyEnter})
	t.Logf("starts with %q", m.rd.top().set.text)
	for range 12 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	for _, r := range "----dfasdfad10e" {
		m = press(t, m, r)
	}
	if s := m.rd.top().set; s.text != "10" {
		t.Fatalf("field %q", s.text)
	}
	m = press(t, m, 'm')
	m = press(t, m, '+')
	if s := m.rd.top().set; s.text != "11m" {
		t.Fatalf("field %q", s.text)
	}
}
