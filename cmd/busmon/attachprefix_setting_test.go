package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/config"
)

// P1-8: attach.prefix is the person's to change in Settings, through the
// same gate agentbus attach applies: a key a client's profile records as
// bound is refused there, naming the client, and nothing is written; a key
// no client binds is written.
func TestSettingsAttachPrefixGate(t *testing.T) {
	old := attachProfiles
	t.Cleanup(func() { attachProfiles = old })
	attachProfiles = func() map[string]agentstate.Profile {
		return map[string]agentstate.Profile{
			"claude": {Client: "claude", Keys: map[string]string{"f7": "bound", "f9": "free"}},
			"codex":  {Client: "codex", Keys: map[string]string{"f7": "bound"}},
			"grok":   {Client: "grok", Keys: map[string]string{"f9": "free"}},
		}
	}
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = showGroup(t, m, config.GroupDisplay)
	edit := func(m Model, key string) Model {
		m = press(t, selectSetting(t, m, "attach.prefix"), tea.Key{Code: tea.KeyEnter})
		for range 8 {
			m = press(t, m, tea.Key{Code: tea.KeyBackspace})
		}
		for _, r := range key {
			m = press(t, m, r)
		}
		return press(t, m, tea.Key{Code: tea.KeyEnter})
	}
	m = edit(m, "f7")
	if e := m.rd.top().set.err; !strings.Contains(e, "f7 is bound by claude, codex") {
		t.Fatalf("a bound key: err %q", e)
	}
	if strings.Contains(configText(t, getenv), "prefix") {
		t.Fatalf("a bound key was written: %q", configText(t, getenv))
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})
	m = edit(m, "f9")
	if got := configText(t, getenv); !strings.Contains(got, `prefix = "f9"`) {
		t.Fatalf("a free key: %q (err %q)", got, m.rd.top().set.err)
	}
}
