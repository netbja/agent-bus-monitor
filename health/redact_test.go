package health

import "testing"

// Redact replaces a whole field on any credential shape, never only the
// matched span (history spec, foureyes: a detection pattern used as a span
// replacer kept the secret in all four of these inputs).
func TestRedactReplacesTheWholeFieldOnAnyHit(t *testing.T) {
	secret := []string{
		"token: abcdef123456",
		"see -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
		"curl -H 'Authorization: Bearer eyJhbGciOi.eyJzdWIi.c2lnbmF0dXJl' https://x",
		"Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc",
		`REDIS_PASSWORD="hunter2"`,
		"redis://:hunter2@localhost:6380",
		"rediss://user:pw@host",
		"https://u:p@example.com/x",
		"GH_TOKEN=ghx123456789",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI",
		`{"password": "x1"}`,
		"api_key=abc",
		"use sk-ant-api03-abcdefghijklmnop",
		"ghp_abcdefghijklmnopqrst",
		"Basic dXNlcjpwYXNzd29yZA==",
		"Basic QWxhZGRpbnNlc2FtZW9wZW5z", // 16+ letters, no digit or symbol
	}
	for _, s := range secret {
		if got := Redact(s); got != Redacted {
			t.Errorf("Redact(%q) = %q, want the whole field %q", s, got, Redacted)
		}
		if SafeText(s) {
			t.Errorf("SafeText accepted %q", s)
		}
	}
	prose := []string{
		"service is ready; restart budget low",
		"the Bearer token is refused",
		"Basic authentication is off",
		"Basic auth over HTTPS",
		"tokens used today: 1200",
		"github reachable over ssh and https",
		"team idle 18m; waiting for you: Reinstall and tag v0.26",
		"https://github.com/netbja/agent-bus-monitor/pull/230",
	}
	for _, s := range prose {
		if got := Redact(s); got != s {
			t.Errorf("Redact(%q) = %q, want it unchanged", s, got)
		}
		if !SafeText(s) {
			t.Errorf("SafeText refused prose %q", s)
		}
	}
}
