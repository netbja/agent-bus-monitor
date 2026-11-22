package config

import "testing"

// Every key says when a change of it takes effect (settings-read-when), and
// every entry names a key: a new key without one fails here.
func TestEveryKeySaysWhenItApplies(t *testing.T) {
	for _, k := range Keys {
		if k.Applies() == AppliesUnknown {
			t.Errorf("%s does not say when a change applies", k.Name)
		}
	}
	for name := range applies {
		if _, ok := Lookup(name); !ok {
			t.Errorf("applies entry %s names no key", name)
		}
	}
}

// The words the person reads, and one key of each kind, from where it is read:
// the dispatcher re-reads the quota thresholds with each reading, builds
// self-repair once at its start, master reads the language at its boot,
// busmon reads display.limit at its start, and request accept reads the wip
// limit each time it runs.
func TestWhenAKeyApplies(t *testing.T) {
	for a, want := range map[Applies]string{AppliesLive: "live", AppliesRun: "at the next run",
		AppliesBusmon: "on busmon restart", AppliesService: "on service restart", AppliesAgent: "on agent restart"} {
		if a.String() != want {
			t.Errorf("%d: %q, want %q", a, a.String(), want)
		}
	}
	for name, want := range map[string]Applies{"dispatch.quota_warn": AppliesLive, "dispatch.stall_after": AppliesLive,
		"dispatch.self_repair": AppliesService, "holder.scrollback": AppliesService, "person.language": AppliesAgent,
		"display.limit": AppliesBusmon, "display.icons": AppliesLive, "board.wip_limit": AppliesRun} {
		if k, _ := Lookup(name); k.Applies() != want {
			t.Errorf("%s applies %q, want %q", name, k.Applies(), want)
		}
	}
}
