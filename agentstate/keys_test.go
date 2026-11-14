package agentstate

import (
	"strings"
	"testing"
)

// The attach prefix's status per client (herdr exit spec 4.6, 4.9): what
// step 3 settled, free or bound, recorded in the profile; a key no profile
// records is unknown, never free.
func TestProfileKeyStatus(t *testing.T) {
	p := Profiles()
	for client, want := range map[string]string{"claude": "free", "grok": "free", "kimi": "unknown", "codex": "unknown", "agy": "unknown"} {
		if got := p[client].KeyStatus("f12"); got != want {
			t.Errorf("%s f12: %s, want %s", client, got, want)
		}
	}
	if got := p["claude"].KeyStatus("ctrl+g"); got != "unknown" {
		t.Errorf("an unrecorded key: %s", got)
	}
	if got := (Profile{}).KeyStatus("f12"); got != "unknown" {
		t.Errorf("no profile: %s", got)
	}
}

// A profile's keys table takes only free or bound, and only key names.
func TestProfileKeysRefused(t *testing.T) {
	base := "client = \"x\"\n[signature]\nprompt = \">\"\nbox_end = \"x\"\n"
	for _, bad := range []string{"[keys]\nf12 = \"maybe\"\n", "[keys]\nhyper = \"free\"\n"} {
		if _, err := parseProfile("x.toml", []byte(base+bad)); err == nil || !strings.Contains(err.Error(), "key") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	p, err := parseProfile("x.toml", []byte(base+"[keys]\nf12 = \"bound\"\n\"ctrl+g\" = \"free\"\n"))
	if err != nil || p.KeyStatus("f12") != "bound" || p.KeyStatus("ctrl+g") != "free" {
		t.Fatalf("%+v %v", p, err)
	}
}
