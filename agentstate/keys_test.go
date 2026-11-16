package agentstate

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/termrec"
)

// The attach prefix's status per client (herdr exit spec 4.6, 4.9): what
// step 3 settled, free or bound, recorded in the profile; a key no profile
// records is unknown, never free.
func TestProfileKeyStatus(t *testing.T) {
	p := Profiles()
	for client, want := range map[string]string{"claude": "free", "grok": "free", "kimi": "unknown", "codex": "free", "agy": "unknown"} {
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

// Codex's F12 is free by the spec's probe (4.6): codex-cli 0.169.0 recorded
// with agentbus term record codex f12-at-input (2026-11-16) writes no
// printable character between the marks around the two F12 presses (legacy,
// then kitty), only redraws of the same frame: the input still reads abc, no
// overlay, no mode change.
func TestCodexF12ProbeWritesNothingVisible(t *testing.T) {
	f, err := os.Open("testdata/raw/codex/f12-at-input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rec, err := termrec.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := rec.OutputBetween("before-f12", "after-f12-kitty")
	if !ok {
		t.Fatal("the recording lacks its marks")
	}
	escapes := regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	if left := escapes.ReplaceAllString(string(out), ""); left != "" {
		t.Fatalf("F12 printed %q", left)
	}
	if !strings.Contains(string(rec.Output()), "abc") {
		t.Fatal("the probe never typed abc")
	}
}
