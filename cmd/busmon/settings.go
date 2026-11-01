package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

type busmonSettings struct {
	PopupAfter  time.Duration
	Snooze      time.Duration
	Bell        bool
	Fade        bool
	AutoAfter   time.Duration
	Theme       string
	Limit       int
	Identity    string
	UpdateCheck bool
	IdleHint    time.Duration // display.idle_hint_after (auto-state; 0 = off)
	Note        string
}

// settingsFlags are busmon's flags over the settings: they win over every
// other source. An empty Theme or As is not given.
type settingsFlags struct {
	LimitSet bool
	Limit    int
	Theme    string
	As       string
}

// loadBusmonSettings resolves busmon's settings through the registry (spec
// docs/superpowers/specs/2026-10-29-config-file-design.md): flag, env,
// config.toml, busmon.toml (legacy), default, per key. A file problem is a
// note, never a failure. An invalid env theme or name keeps its raw value, so
// the validation after this stops busmon as it always has.
func loadBusmonSettings(getenv func(string) string, flags settingsFlags) busmonSettings {
	cfg := config.Load(config.Path(getenv), "config.toml")
	legacy := config.LoadLegacy(config.LegacyPath(getenv), "busmon.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{cfg, legacy}}
	s := busmonSettings{
		PopupAfter: r.Duration("waiting.popup_after"), Snooze: r.Duration("waiting.snooze"),
		Bell: r.Bool("waiting.bell"), Fade: r.Bool("waiting.fade"), AutoAfter: r.Duration("recap.auto_after"),
		Limit: r.Int("display.limit"), UpdateCheck: r.Bool("updates.check"), IdleHint: r.Duration("display.idle_hint_after"),
	}
	raw := func(name string) string { k, _ := config.Lookup(name); return getenv(k.Env) }
	if t := r.Get("display.theme"); t.Invalid {
		s.Theme = raw("display.theme")
	} else {
		s.Theme = t.Value.(string)
	}
	if as := r.Get("identity.as"); as.Invalid {
		s.Identity = raw("identity.as")
	} else {
		s.Identity = as.Value.(string)
	}
	if flags.LimitSet {
		s.Limit = flags.Limit
	}
	if flags.Theme != "" {
		s.Theme = flags.Theme
	}
	if flags.As != "" {
		s.Identity = flags.As
	}
	var notes []string
	for _, f := range []*config.File{cfg, legacy} {
		for _, w := range f.Warnings {
			notes = append(notes, w.String())
		}
	}
	s.Note = strings.Join(notes, "; ")
	return s
}

func oneOf(s string, options ...string) bool {
	for _, option := range options {
		if s == option {
			return true
		}
	}
	return false
}

func ifSet(set bool, value string) string {
	if set {
		return value
	}
	return ""
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

func (s busmonSettings) validateIdentity() error {
	if s.Identity != "" && !bus.ValidName(s.Identity) {
		return fmt.Errorf("invalid human identity %q", s.Identity)
	}
	return nil
}
