package setup

import (
	"encoding/json"
	"regexp"
	"strings"
)

// KeybindBlock is setup.sh's binding of prefix+up to the Projects picker,
// appended to herdr's config; OldKeybindBlock is the herdr-plus one an
// older setup wrote, replaced only when byte for byte unedited. Both start
// with the blank line that separates them from the person's config.
const KeybindBlock = `
# agentbus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "netbja.agentbus.projects"
description = "agent-bus: projects"`

const OldKeybindBlock = `
# herdr-plus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "cloudmanic.herdr-plus.projects"
description = "herdr-plus: projects"`

// PluginRow is one entry of herdr plugin list --json.
type PluginRow struct {
	ID      string `json:"plugin_id"`
	Enabled bool   `json:"enabled"`
	Root    string `json:"plugin_root"`
	Version string `json:"version"`
}

// PluginRows reads herdr plugin list --json; ok is false when the output is
// no listing (setup.sh's plugin_rows without its #plugins line).
func PluginRows(out []byte) ([]PluginRow, bool) {
	var v struct {
		Result *struct {
			Plugins *[]PluginRow `json:"plugins"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &v) != nil || v.Result == nil || v.Result.Plugins == nil {
		return nil, false
	}
	return *v.Result.Plugins, true
}

// Ours is this checkout's plugin id's row, when registered.
func Ours(rows []PluginRow) (PluginRow, bool) {
	for _, r := range rows {
		if r.ID == PluginID {
			return r, true
		}
	}
	return PluginRow{}, false
}

// KeybindSwap is setup.sh's keybind_swap: content with every exact copy of
// old (its lines after the leading blank one) replaced by new's lines, or,
// when new is empty, dropped with the blank line before it. ok is false when
// content holds no exact copy: an edited block is left alone, since guessing
// where it ends could take the person's next section with it.
func KeybindSwap(old, new, content string) (string, bool) {
	want := strings.Split(old, "\n")[1:]
	var repl []string
	if new != "" {
		repl = strings.Split(new, "\n")[1:]
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	start, skip := map[int]bool{}, map[int]bool{}
	found := false
	for i := 0; i+len(want) <= len(lines); i++ {
		match := true
		for k, w := range want {
			if lines[i+k] != w {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		start[i] = true
		for k := range want {
			skip[i+k] = true
		}
		if len(repl) == 0 && i > 0 && lines[i-1] == "" {
			skip[i-1] = true
		}
		found = true
		i += len(want) - 1
	}
	if !found {
		return content, false
	}
	var b strings.Builder
	for i, l := range lines {
		if start[i] {
			for _, r := range repl {
				b.WriteString(r + "\n")
			}
		}
		if !skip[i] {
			b.WriteString(l + "\n")
		}
	}
	return b.String(), true
}

var projectsAction = regexp.MustCompile(`"action_id":"projects"[^}]*"plugin_id":"` + regexp.QuoteMeta(PluginID) + `"`)

// pluginActionState is setup.sh's plugin_action_state on herdr plugin action
// list's output: ok, noserver (no herdr server to ask, which says nothing of
// the plugin) or missing.
func pluginActionState(out string) string {
	out = strings.ReplaceAll(out, "\n", "")
	switch {
	case projectsAction.MatchString(out):
		return "ok"
	case strings.Contains(out, `"server_not_running"`):
		return "noserver"
	}
	return "missing"
}
