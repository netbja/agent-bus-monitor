package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// settings-read-when: the detail line says when a change of the key applies,
// and saving a key busmon does not read says when it applies, never "next
// start of busmon".
func TestSettingsSayWhenAChangeApplies(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	groups := settingsGroups(getenv, settingsFlags{}, settingsPageGroups)
	for key, want := range map[string]string{"dispatch.self_repair": "applies on service restart", "display.icons": "applies live",
		"board.wip_limit": "applies at the next run", "display.limit": "applies on busmon restart"} {
		detail := tokLinesText(wrapSpans(settingsDetail(settingRowOf(t, groups, key)), 200, "", ""))
		if !strings.Contains(detail, want) {
			t.Errorf("%s detail lacks %q: %s", key, want, detail)
		}
	}
	m := &Model{cfg: shellConfig{getenv: getenv}}
	for key, want := range map[string]string{"dispatch.self_repair": "saved: applies on service restart",
		"dispatch.quota_warn": "saved: applies live", "board.wip_limit": "saved: applies at the next run",
		"display.limit": "saved: next start of busmon", "person.language": "saved: applies on agent restart",
		"sentinel.reload": "saved: applies live"} {
		if got := m.applySettings(key); got != want {
			t.Errorf("save %s: %q, want %q", key, got, want)
		}
	}
}

// What busmon applies at once is live in the registry, and a key busmon
// reads that the registry calls live is one busmon applies at once: the
// registry cannot drift from busmon's own switch.
func TestBusmonAppliesAgreeWithTheRegistry(t *testing.T) {
	m := &Model{cfg: shellConfig{getenv: settingsEnv(t, "", "", nil)}}
	for _, k := range config.Keys {
		if k.Name == "display.theme" || k.Name == "display.follow" { // applied at once with a terminal and its Activity list; m has neither
			continue
		}
		said := m.applySettings(k.Name)
		applied := strings.Contains(said, "applied")
		if applied && k.Applies() != config.AppliesLive {
			t.Errorf("%s: busmon applies it at once (%q), the registry says %q", k.Name, said, k.Applies())
		}
		if !applied && k.Applies() == config.AppliesLive && strings.Contains(k.ReadBy, "busmon") {
			t.Errorf("%s: the registry says live, busmon says %q", k.Name, said)
		}
	}
}
