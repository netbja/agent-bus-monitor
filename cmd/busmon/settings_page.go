package main

import (
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// The settings page (spec docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md):
// the registry's keys by group, one row each, saying what value applies and
// where it comes from, in agentbus config's words.

// settingRow is one key on the page.
type settingRow struct {
	Key      string
	Sym      string // ● config.toml, ○ the default, · elsewhere (read-only here), ▲ refused
	Label    string
	Value    string // the value that applies, in the person's words
	Source   string
	Editable bool // the page may write it: nothing above config.toml sets it
	// PersonOnly is a person's own setting (backup.after_*): shown, never
	// written here (edit config.toml yourself).
	PersonOnly bool
	InFile     bool // config.toml holds the key (● or a refused line): d may unset it
	// AfterUnset is what applies once config.toml's line is gone ("busmon.toml's
	// light", "the default auto"): d's confirmation names it (spec).
	AfterUnset string
}

// settingGroup is one group's rows, in the registry's order.
type settingGroup struct {
	Group config.Group
	Rows  []settingRow
}

// settingLabels are the page's words for a key; one missing reads as the key.
var settingLabels = map[string]string{
	"display.theme": "Theme", "display.icons": "Icons", "display.limit": "Activity lines at start", "display.idle_hint_after": "Idle? hint after",
	"waiting.fade": "Fade and slide", "waiting.popup_after": "Reminder after", "waiting.snooze": "Reminder snooze",
	"waiting.bell": "Bell", "recap.auto_after": "Recap after away", "updates.check": "Update check", "identity.as": "Your name", "start.team": "Default team",
	"backup.after_command": "Command after each backup", "backup.after_env": "Variables it keeps", "backup.after_timeout": "Its time limit",
}

// settingOnPage: the Backup group shows only its person-only keys, read-only
// (backup spec); every key of the other groups shows.
func settingOnPage(k *config.Key) bool { return k.Group() != config.GroupBackup || k.PersonOnly }

// settingFlags are the flags above a key's env: busmon's own, read-only here.
var settingFlags = map[string]string{"display.theme": "busmon --theme", "display.limit": "busmon --limit", "identity.as": "busmon --as"}

// settingsGroups resolves the keys of groups as busmon does (flag, env,
// config.toml, busmon.toml, the default) and lays out one row each.
func settingsGroups(getenv func(string) string, flags settingsFlags, groups []config.Group) []settingGroup {
	cfg := config.Load(config.Path(getenv), "config.toml")
	legacy := config.LoadLegacy(config.LegacyPath(getenv), "busmon.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{cfg, legacy}}
	below := config.Resolver{Getenv: getenv, Files: []*config.File{legacy}} // without config.toml
	refused := map[string]int{}                                             // config.toml's refused lines, by key
	for _, w := range cfg.Warnings {
		if w.Key != "" {
			refused[w.Key] = w.Line
		}
	}
	flagged := map[string]any{}
	if flags.Theme != "" {
		flagged["display.theme"] = flags.Theme
	}
	if flags.LimitSet {
		flagged["display.limit"] = flags.Limit
	}
	if flags.As != "" {
		flagged["identity.as"] = flags.As
	}
	var out []settingGroup
	for _, g := range groups {
		sg := settingGroup{Group: g}
		for _, k := range config.Keys {
			if k.Group() != g || !settingOnPage(k) {
				continue
			}
			row := settingRow{Key: k.Name, Label: settingLabels[k.Name], Editable: !k.PersonOnly, PersonOnly: k.PersonOnly}
			if row.Label == "" {
				row.Label = k.Name
			}
			res := r.Get(k.Name)
			value := res.Value
			switch v, isFlag := flagged[k.Name]; {
			case isFlag:
				row.Sym, row.Source, row.Editable, value = GlyphNone, "flag "+settingFlags[k.Name], false, v
			case res.Invalid:
				row.Sym, row.Source, row.Editable = GlyphBlocked, res.Source+" refused", false
			case res.Source != "default" && res.Source != "config.toml" && res.Source != "busmon.toml":
				row.Sym, row.Source, row.Editable = GlyphNone, res.Source, false // the env: only unsetting it lets a file apply
			case refused[k.Name] > 0:
				row.Sym, row.Source, row.InFile = GlyphBlocked, fmt.Sprintf("config.toml line %d refused", refused[k.Name]), true
			case res.Source == "config.toml":
				row.Sym, row.Source, row.InFile = GlyphWorking, "config.toml", true
			case res.Source == "busmon.toml":
				row.Sym, row.Source = GlyphNone, "busmon.toml (legacy)"
			default:
				row.Sym, row.Source = GlyphIdle, "default"
			}
			row.Value = settingValue(k, value)
			switch after := below.Get(k.Name); after.Source {
			case "default":
				row.AfterUnset = "the default " + settingValue(k, after.Value)
			case "busmon.toml":
				row.AfterUnset = "busmon.toml's " + settingValue(k, after.Value)
			default:
				row.AfterUnset = after.Source + "'s " + settingValue(k, after.Value)
			}
			sg.Rows = append(sg.Rows, row)
		}
		out = append(out, sg)
	}
	return out
}

// settingValue is v in the person's words: on/off, a duration as written, 0
// as off where the key reads it so, display.limit at or below 0 as all.
func settingValue(k *config.Key, v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "on"
		}
		return "off"
	case time.Duration:
		if x == 0 {
			return "off"
		}
	case int:
		if k.Name == "display.limit" && x <= 0 {
			return "all" // busmon's backfill replays all history for limit <= 0
		}
	case string:
		if x == "" {
			return "none"
		}
	}
	return config.Format(v)
}
