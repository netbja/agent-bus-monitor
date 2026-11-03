package main

import (
	"fmt"
	"path/filepath"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/notify"
)

// notifyTargetLines are the settings page's lines under the Notifications
// rows (notifications spec, "busmon"): one per target of notify.toml, its
// kind, whether notify.targets lists it and its last outcome; never its URL,
// topic, token or webhook. notify.toml is the person's: read here, never
// written (edit it yourself).
func notifyTargetLines(getenv func(string) string) []string {
	f := notify.Load(notify.Path(getenv))
	switch {
	case !f.Exists:
		return []string{"no notify.toml: copy a table from the guide (edit it yourself)"}
	case f.Refused != "":
		return []string{"notify.toml refused: " + f.Refused}
	}
	listed := map[string]bool{}
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	for _, n := range config.SplitList(r.String("notify.targets")) {
		listed[n] = true
	}
	var last notify.Last
	unread := false // the record cannot be read: no outcome is claimed (reviewer-codex)
	if d := notifyStateDir(getenv); d == "" {
		unread = true
	} else if l, err := notify.ReadLast(notify.LastPath(d)); err != nil {
		unread = true
	} else {
		last = l
	}
	var out []string
	for i, t := range f.Targets {
		state := "not listed in Targets"
		if listed[t.Name] && unread {
			state = "last send unknown (notify-last.json cannot be read)"
		} else if listed[t.Name] {
			state = "never sent"
			if o, ok := last.Latest(t.Name); ok {
				state = "last sent " + o.At.Local().Format("15:04") + " ok"
				if !o.OK {
					state = "last send failed " + o.At.Local().Format("15:04") + " (" + o.Err + ")"
				}
			}
		}
		line := fmt.Sprintf("%-8s %-8s %s", t.Name, t.Kind, state)
		if i == 0 {
			line += "   (notify.toml, edit it yourself)"
		}
		out = append(out, line)
	}
	return out
}

// notifyStateDir is <state> (notify-last.json's folder), as agentbus reads it.
func notifyStateDir(getenv func(string) string) string {
	if d := getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "state", "agentbus")
	}
	return ""
}
