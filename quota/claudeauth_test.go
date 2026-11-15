package quota

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// authLine is the entry Claude Code writes when its login has expired:
// isApiErrorMessage and error set by the client, model <synthetic>.
func authLine(at time.Time) string {
	return `{"type":"assistant","timestamp":"` + at.UTC().Format(time.RFC3339Nano) +
		`","isApiErrorMessage":true,"error":"authentication_failed","message":{"model":"<synthetic>","content":[{"type":"text","text":"Please run /login"}]}}`
}

// servedLine is an answer the API really gave.
func servedLine(at time.Time) string {
	return `{"type":"assistant","timestamp":"` + at.UTC().Format(time.RFC3339Nano) +
		`","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"done"}]}}`
}

func writeSession(t *testing.T, root, name string, mod time.Time, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// login-updates 2.1: an authentication_failed error entry in a recent
// transcript reads expired, at its time; a served answer newer than it (a
// /login in any session) clears it, an older one does not; text that only
// says "Login expired" reads nothing; a file older than 6 h is not read.
func TestClaudeLoginExpired(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	for _, c := range []struct {
		name  string
		mod   time.Time
		lines []string
		want  bool
	}{
		{"expired", now, []string{servedLine(at.Add(-time.Minute)), authLine(at)}, true},
		{"served since", now, []string{authLine(at), servedLine(at.Add(time.Minute))}, false},
		{"only text", now, []string{`{"type":"assistant","timestamp":"` + at.Format(time.RFC3339) +
			`","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"Login expired, error authentication_failed"}]}}`}, false},
		{"model not synthetic", now, []string{strings.Replace(authLine(at), "<synthetic>", "claude-opus-5-5", 1)}, false},
		{"not an api error", now, []string{strings.Replace(authLine(at), `"isApiErrorMessage":true,`, "", 1)}, false},
		{"another error", now, []string{strings.Replace(authLine(at), "authentication_failed", "server_error", 1)}, false},
		{"another error quoting it", now, []string{strings.NewReplacer(`"error":"authentication_failed"`, `"error":"server_error"`,
			"Please run /login", "authentication_failed").Replace(authLine(at))}, false},
		{"older than 6 h", now.Add(-7 * time.Hour), []string{authLine(at)}, false},
	} {
		root := t.TempDir()
		writeSession(t, root, "s1", c.mod, c.lines...)
		got, ok := ClaudeLoginExpired(root, now)
		if ok != c.want || (ok && !got.Equal(at)) {
			t.Errorf("%s: expired %v at %v, want %v at %v", c.name, ok, got, c.want, at)
		}
	}
}

// The login is the account's: an answer served in another session clears an
// expiry seen in one.
func TestClaudeLoginExpiredClearedByAnotherSession(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	writeSession(t, root, "a", now, authLine(now.Add(-time.Hour)))
	if _, ok := ClaudeLoginExpired(root, now); !ok {
		t.Fatal("the expiry alone is not read")
	}
	writeSession(t, root, "b", now, servedLine(now.Add(-time.Minute)))
	if _, ok := ClaudeLoginExpired(root, now); ok {
		t.Fatal("a newer answer in another session does not clear it")
	}
}
