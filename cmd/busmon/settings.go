package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/bus"
)

type busmonFileConfig struct {
	Waiting struct {
		PopupAfter string `toml:"popup_after"`
		Snooze     string `toml:"snooze"`
		Bell       *bool  `toml:"bell"`
		Fade       *bool  `toml:"fade"`
	} `toml:"waiting"`
	Recap struct {
		AutoAfter string `toml:"auto_after"`
	} `toml:"recap"`
	Display struct {
		Theme *string `toml:"theme"`
		Limit *int    `toml:"limit"`
	} `toml:"display"`
	Identity struct {
		As *string `toml:"as"`
	} `toml:"identity"`
}

type busmonSettings struct {
	PopupAfter time.Duration
	Snooze     time.Duration
	Bell       bool
	Fade       bool
	AutoAfter  time.Duration
	Theme      string
	Limit      int
	Identity   string
	Note       string
}

type settingsOverrides struct {
	LimitSet  bool
	LimitFlag int
	LimitEnv  string
	ThemeFlag string
	ThemeEnv  string
	AsFlag    string
	AsEnv     string
}

func defaultBusmonSettings() busmonSettings {
	return busmonSettings{PopupAfter: 5 * time.Minute, Snooze: 15 * time.Minute, Bell: true, Fade: true,
		AutoAfter: 8 * time.Hour, Theme: "auto", Limit: 25}
}

func busmonConfigPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "agentbus", "busmon.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agentbus", "busmon.toml")
}

func resolveBusmonSettings(file busmonFileConfig, overrides settingsOverrides) busmonSettings {
	s := defaultBusmonSettings()
	var notes []string
	setDuration := func(name, raw string, dst *time.Duration) {
		if raw == "" {
			return
		}
		if raw == "0" {
			*dst = 0
			return
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			notes = append(notes, "invalid "+name)
			return
		}
		*dst = d
	}
	setDuration("waiting.popup_after", file.Waiting.PopupAfter, &s.PopupAfter)
	setDuration("waiting.snooze", file.Waiting.Snooze, &s.Snooze)
	setDuration("recap.auto_after", file.Recap.AutoAfter, &s.AutoAfter)
	if file.Waiting.Bell != nil {
		s.Bell = *file.Waiting.Bell
	}
	if file.Waiting.Fade != nil {
		s.Fade = *file.Waiting.Fade
	}
	if file.Display.Theme != nil {
		if oneOf(*file.Display.Theme, "auto", "dark", "light") {
			s.Theme = *file.Display.Theme
		} else {
			notes = append(notes, "invalid display.theme")
		}
	}
	if file.Display.Limit != nil {
		if *file.Display.Limit >= 0 {
			s.Limit = *file.Display.Limit
		} else {
			notes = append(notes, "invalid display.limit")
		}
	}
	if file.Identity.As != nil {
		if *file.Identity.As == "" || bus.ValidName(*file.Identity.As) {
			s.Identity = *file.Identity.As
		} else {
			notes = append(notes, "invalid identity.as")
		}
	}
	if overrides.LimitSet {
		s.Limit = overrides.LimitFlag
	} else if raw := strings.TrimSpace(overrides.LimitEnv); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			s.Limit = n
		}
	}
	if overrides.ThemeFlag != "" {
		s.Theme = overrides.ThemeFlag
	} else if overrides.ThemeEnv != "" {
		s.Theme = overrides.ThemeEnv
	}
	if overrides.AsFlag != "" {
		s.Identity = overrides.AsFlag
	} else if overrides.AsEnv != "" {
		s.Identity = overrides.AsEnv
	}
	s.Note = strings.Join(notes, ", ")
	return s
}

func loadBusmonSettings(path string, overrides settingsOverrides) busmonSettings {
	var file busmonFileConfig
	if _, err := toml.DecodeFile(path, &file); err != nil && !os.IsNotExist(err) {
		s := resolveBusmonSettings(busmonFileConfig{}, overrides)
		if s.Note != "" {
			s.Note = "busmon.toml ignored; " + s.Note
		} else {
			s.Note = "busmon.toml ignored"
		}
		return s
	}
	return resolveBusmonSettings(file, overrides)
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

func (s busmonSettings) validateIdentity() error {
	if s.Identity != "" && !bus.ValidName(s.Identity) {
		return fmt.Errorf("invalid human identity %q", s.Identity)
	}
	return nil
}
