package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/quota"
)

// Quota settings step 1 (§2.3): busmon's account cards date a reset as the
// dispatcher's messages do: tomorrow's 13:59 seen at 14:30 read '13:59'.
func TestResetClockDatesTheReset(t *testing.T) {
	t.Cleanup(func() { dateFormat = quota.DateRelative })
	now := time.Date(2026, 11, 16, 14, 30, 0, 0, time.Local)
	tomorrow := time.Date(2026, 11, 17, 13, 59, 0, 0, time.Local)
	if got := resetClock(tomorrow, now); got != "tomorrow 13:59" {
		t.Fatalf("relative: %q", got)
	}
	dateFormat = quota.DateClock12
	if got := resetClock(tomorrow, now); got != "tomorrow 1:59 PM" {
		t.Fatalf("clock12: %q", got)
	}
}

// display.date_format is a Display row chosen with ←→ among the three
// formats, each with a sample; a change applies at the next redraw.
func TestSettingsDateFormat(t *testing.T) {
	t.Cleanup(func() { dateFormat = quota.DateRelative })
	if c := settingChoices["display.date_format"]; strings.Join(c, ",") != "relative,iso,clock12" {
		t.Fatalf("choices %v", c)
	}
	help := settingHelps["display.date_format"]
	for _, want := range []string{"tomorrow 13:59", "2026-11-17 13:59", "tomorrow 1:59 PM"} {
		if !strings.Contains(help, want) {
			t.Errorf("help %q lacks the sample %q", help, want)
		}
	}
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = showGroup(t, m, config.GroupDisplay)
	m = press(t, selectSetting(t, m, "display.date_format"), tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight}) // relative -> iso
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if got := configText(t, getenv); !strings.Contains(got, `date_format = "iso"`) {
		t.Fatalf("file %q (err %q)", got, m.rd.top().set.err)
	}
	if dateFormat != quota.DateISO || m.cfg.settings.DateFormat != quota.DateISO {
		t.Fatalf("not applied: %q %q", dateFormat, m.cfg.settings.DateFormat)
	}
}
