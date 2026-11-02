package main

import (
	"strings"
	"testing"
)

// An env value its rule does not take is skipped, and said: the warning names
// the variable and the source that applies, never the value (autoclear-default).
func TestSettingsWarningsNameASkippedEnv(t *testing.T) {
	for _, v := range []string{"true", "yes", "garbage-x7q"} {
		ws := settingsWarnings(withConfig(t, "", map[string]string{"AGENT_BUS_AUTO_CLEAR": v}))
		var hit string
		for _, w := range ws {
			if strings.Contains(w, "AGENT_BUS_AUTO_CLEAR") {
				hit = w
			}
		}
		if hit == "" {
			t.Fatalf("%q: no warning naming AGENT_BUS_AUTO_CLEAR in %q", v, ws)
		}
		if strings.Contains(hit, v) {
			t.Errorf("%q: the warning echoes the value: %q", v, hit)
		}
		if !strings.Contains(hit, "default applies") {
			t.Errorf("%q: the warning does not say what applies: %q", v, hit)
		}
	}
	for _, v := range []string{"0", "1"} {
		for _, w := range settingsWarnings(withConfig(t, "", map[string]string{"AGENT_BUS_AUTO_CLEAR": v})) {
			if strings.Contains(w, "AGENT_BUS_AUTO_CLEAR") {
				t.Errorf("%q is taken, yet warned: %q", v, w)
			}
		}
	}
}
