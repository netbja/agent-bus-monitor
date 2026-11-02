package config

import "testing"

// Every key has its group on the settings page (spec, "Groups"), and every
// group entry names a key: a new key without one fails here, as an env name
// written outside the registry fails TestNoEnvLiteralOutsideTheRegistry.
func TestEveryKeyHasItsGroup(t *testing.T) {
	for _, k := range Keys {
		if k.Group() == "" {
			t.Errorf("%s has no group", k.Name)
		}
	}
	for name := range groups {
		if _, ok := Lookup(name); !ok {
			t.Errorf("group entry %s names no key", name)
		}
	}
	for name, want := range map[string]Group{"display.theme": GroupDisplay, "waiting.fade": GroupDisplay, "waiting.bell": GroupAttention,
		"recap.auto_after": GroupAttention, "updates.check": GroupAttention, "identity.as": GroupIdentity, "dispatch.quota": GroupServices,
		"history.keep_days": GroupServices, "health.runaway.screen_repeats": GroupHealth, "bus.verdict_max": GroupBusCaps} {
		if k, _ := Lookup(name); k.Group() != want {
			t.Errorf("%s in %q, want %q", name, k.Group(), want)
		}
	}
}
