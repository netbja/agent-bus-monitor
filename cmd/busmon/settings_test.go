package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBusmonSettingsPrecedence(t *testing.T) {
	file := busmonFileConfig{}
	file.Waiting.PopupAfter, file.Waiting.Snooze = "2m", "3m"
	file.Recap.AutoAfter = "4h"
	theme, limit, identity := "light", 12, "fileperson"
	file.Display.Theme, file.Display.Limit, file.Identity.As = &theme, &limit, &identity
	got := resolveBusmonSettings(file, settingsOverrides{
		LimitSet: true, LimitFlag: 7, LimitEnv: "8", ThemeFlag: "dark", ThemeEnv: "auto", AsFlag: "cli", AsEnv: "env",
	})
	if got.Limit != 7 || got.Theme != "dark" || got.Identity != "cli" {
		t.Fatalf("explicit flags did not win: %+v", got)
	}
	got = resolveBusmonSettings(file, settingsOverrides{LimitEnv: "8", ThemeEnv: "auto", AsEnv: "env"})
	if got.Limit != 8 || got.Theme != "auto" || got.Identity != "env" {
		t.Fatalf("environment did not win over file: %+v", got)
	}
	got = resolveBusmonSettings(file, settingsOverrides{})
	if got.Limit != 12 || got.Theme != "light" || got.Identity != "fileperson" || got.PopupAfter != 2*time.Minute || got.Snooze != 3*time.Minute || got.AutoAfter != 4*time.Hour {
		t.Fatalf("file values not applied: %+v", got)
	}
	got = resolveBusmonSettings(busmonFileConfig{}, settingsOverrides{})
	if got.Limit != 25 || got.Theme != "auto" || got.PopupAfter != 5*time.Minute || got.Snooze != 15*time.Minute || got.AutoAfter != 8*time.Hour || !got.Bell || !got.Fade {
		t.Fatalf("defaults changed: %+v", got)
	}
}

func TestLoadBusmonSettingsInvalidFileDoesNotFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busmon.toml")
	if err := os.WriteFile(path, []byte("[waiting\npopup_after = nope"), 0600); err != nil {
		t.Fatal(err)
	}
	got := loadBusmonSettings(path, settingsOverrides{ThemeFlag: "light"})
	if got.Theme != "light" || got.PopupAfter != 5*time.Minute || !strings.Contains(got.Note, "ignored") {
		t.Fatalf("invalid file blocked or corrupted settings: %+v", got)
	}
}

func TestBusmonSettingsInvalidValuesFallBack(t *testing.T) {
	file := busmonFileConfig{}
	file.Waiting.PopupAfter = "lots"
	file.Waiting.Bell = new(bool)
	*file.Waiting.Bell = false
	bad := "neon"
	file.Display.Theme = &bad
	limit := -1
	file.Display.Limit = &limit
	got := resolveBusmonSettings(file, settingsOverrides{})
	if got.PopupAfter != 5*time.Minute || got.Theme != "auto" || got.Limit != 25 || got.Bell || !strings.Contains(got.Note, "invalid waiting.popup_after") || !strings.Contains(got.Note, "invalid display.theme") {
		t.Fatalf("invalid values did not fall back with a note: %+v", got)
	}
}
