package main

import "testing"

// busmon-cli-flags item 3 (the person's option A): busmon's name is --as,
// then identity.as, then person.name, then "pilot" (humanIdentity); an
// invalid person.name is skipped, never a refusal to start.
func TestIdentityFallsBackToPersonName(t *testing.T) {
	for _, c := range []struct {
		name, toml string
		env        map[string]string
		flag       string
		want       string
	}{
		{"person.name alone", "[person]\nname = \"charlie\"\n", nil, "", "charlie"},
		{"person.name by env", "", map[string]string{"AGENT_BUS_PERSON_NAME": "dana"}, "", "dana"},
		{"identity.as wins", "[person]\nname = \"charlie\"\n[identity]\nas = \"ops\"\n", nil, "", "ops"},
		{"AGENT_BUS_HUMAN wins", "[person]\nname = \"charlie\"\n", map[string]string{"AGENT_BUS_HUMAN": "eve"}, "", "eve"},
		{"--as wins", "[person]\nname = \"charlie\"\n[identity]\nas = \"ops\"\n", nil, "flo", "flo"},
		{"nothing set", "", nil, "", defaultHuman},
		{"invalid person.name skipped", "", map[string]string{"AGENT_BUS_PERSON_NAME": "Not Valid"}, "", defaultHuman},
	} {
		s := loadBusmonSettings(settingsDir(t, c.toml, "", c.env), settingsFlags{As: c.flag})
		got, err := humanIdentity(s.Identity, "")
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
}
