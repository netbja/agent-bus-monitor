package agentstate

import "testing"

// [login] source (login-updates §3.4): only a known source is accepted, and
// only claude.toml declares one, so Codex, agy, Kimi and Grok are not checked.
func TestLoginSource(t *testing.T) {
	good := "client = \"x\"\n[signature]\nprompt = '^>'\nbox_end = '^-'\nready = ['>']\n"
	p, err := parseProfile("x.toml", []byte(good+"[login]\nsource = \"claude-credentials\"\n"))
	if err != nil || p.LoginSource != LoginClaudeCredentials {
		t.Fatalf("%+v %v", p.LoginSource, err)
	}
	for name, text := range map[string]string{
		"unknown source": good + "[login]\nsource = \"codex-auth\"\n",
		"unknown key":    good + "[login]\nfile = \"x\"\n",
	} {
		if _, err := parseProfile("x.toml", []byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if p, err := parseProfile("x.toml", []byte(good)); err != nil || p.LoginSource != "" {
		t.Fatalf("no [login]: %q %v", p.LoginSource, err)
	}
	for client, p := range Profiles() {
		want := ""
		if client == "claude" {
			want = LoginClaudeCredentials
		}
		if p.LoginSource != want {
			t.Errorf("%s: login source %q, want %q", client, p.LoginSource, want)
		}
	}
}
