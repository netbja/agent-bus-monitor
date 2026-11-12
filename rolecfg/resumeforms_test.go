package rolecfg

import (
	"strings"
	"testing"
)

// Every client has its resume form recorded as data (self-repair step 5,
// herdr-exit §4.9), and only a client whose session id source was verified
// on this host resumes: Claude Code (its SessionStart hook). Codex, Kimi,
// Grok and agy start fresh until their probe runs; Grok's /new and /clear
// start a session only its (untrusted until the person trusts it) hook sees.
func TestClientResumeForms(t *testing.T) {
	verified := map[string]bool{}
	for _, c := range Clients {
		if c.Resume == "" {
			t.Errorf("%s: no resume form", c.Client)
		}
		if c.ResumeSource == "" {
			t.Errorf("%s: no session id source", c.Client)
		}
		verified[c.Client] = ResumeVerified(c.Client)
	}
	want := map[string]bool{"claude-code": true, "codex": false, "kimi-cli": false, "grok": false, "agy": false}
	for client, v := range want {
		if verified[client] != v {
			t.Errorf("ResumeVerified(%s) = %v, want %v", client, verified[client], v)
		}
	}
	if ResumeVerified("") || ResumeVerified("extra") {
		t.Error("an unknown client resumes")
	}
}

// An unverified client is refused a resume: it starts fresh, never with
// another flag that guesses (--continue picks the folder's latest session).
func TestUnverifiedClientsRefuseResume(t *testing.T) {
	for _, client := range []string{"codex", "kimi-cli", "grok"} {
		f := newLaunchFixture(t)
		_, _, err := f.launchWithResume(f.config(client, "", "", ""), "coder", "session-123")
		if err == nil || !strings.Contains(err.Error(), "session resume is not verified for "+client) {
			t.Fatalf("%s resume: %v", client, err)
		}
	}
}
