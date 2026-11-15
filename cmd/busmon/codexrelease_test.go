package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
)

// npmRegistry answers @openai/codex/latest with version, counting requests.
func npmRegistry(t *testing.T, version string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/@openai/codex/latest" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"name":"@openai/codex","version":"` + version + `","description":"x"}`))
	}))
	t.Cleanup(s.Close)
	return s, &calls
}

// codexAgent is a Codex agent whose launched client's package.json says label.
func codexAgent(label string) bus.AgentSnapshot {
	return bus.AgentSnapshot{Client: "codex", ClientFP: clientfp.Fingerprint{Path: "/usr/bin/codex", Label: label}.Encode()}
}

// login-updates 4: the line shows only when the published version is newer
// than the installed one; bare npm versions compare as releases.
func TestCodexNotice(t *testing.T) {
	want := "codex 0.171.0 available (installed 0.170.0): npm install -g @openai/codex@latest"
	if got := codexNotice("0.170.0", "0.171.0"); got != want {
		t.Fatalf("newer: %q", got)
	}
	for _, c := range [][2]string{{"0.171.0", "0.171.0"}, {"0.171.0", "0.170.0"}, {"", "0.171.0"}, {"0.170.0", ""}, {"0.170.0", "garbage"}} {
		if got := codexNotice(c[0], c[1]); got != "" {
			t.Errorf("installed %q published %q: %q", c[0], c[1], got)
		}
	}
}

// The installed version is a Codex agent's client_fp label; with several, the
// oldest (the one an install would update); no Codex agent or no label: none.
func TestCodexInstalled(t *testing.T) {
	snaps := map[string]bus.AgentSnapshot{
		"coder":  {Client: "claude-code", ClientFP: clientfp.Fingerprint{Path: "/c", Label: "9.9.9"}.Encode()},
		"rev":    codexAgent("0.170.0"),
		"rev2":   codexAgent("0.168.2"),
		"nolab":  codexAgent(""),
		"broken": {Client: "codex", ClientFP: "{"},
	}
	if got := codexInstalled(snaps); got != "0.168.2" {
		t.Fatalf("installed %q", got)
	}
	delete(snaps, "rev")
	delete(snaps, "rev2")
	if got := codexInstalled(snaps); got != "" {
		t.Fatalf("no labelled codex agent: %q", got)
	}
}

// The registry is asked at most once a day (cache file), field version only;
// offline is silent and cached for the day.
func TestLatestCodexIsCachedForADay(t *testing.T) {
	s, calls := npmRegistry(t, "0.171.0")
	path := filepath.Join(t.TempDir(), "agentbus", "client-codex.json")
	now := time.Now()
	ctx := context.Background()
	if v := latestCodex(ctx, s.Client(), s.URL+"/@openai/codex/latest", path, now); v != "0.171.0" {
		t.Fatalf("published %q", v)
	}
	if v := latestCodex(ctx, s.Client(), s.URL+"/@openai/codex/latest", path, now.Add(23*time.Hour)); v != "0.171.0" || calls.Load() != 1 {
		t.Fatalf("within the day: %q after %d requests", v, calls.Load())
	}
	latestCodex(ctx, s.Client(), s.URL+"/@openai/codex/latest", path, now.Add(25*time.Hour))
	if calls.Load() != 2 {
		t.Fatalf("%d requests after a day, want 2", calls.Load())
	}
	s.Close()
	off := filepath.Join(t.TempDir(), "c.json")
	if v := latestCodex(ctx, s.Client(), s.URL+"/@openai/codex/latest", off, now); v != "" {
		t.Fatalf("offline: %q", v)
	}
	s2, calls2 := npmRegistry(t, "0.171.0")
	if v := latestCodex(ctx, s2.Client(), s2.URL+"/@openai/codex/latest", off, now.Add(time.Hour)); v != "" || calls2.Load() != 0 {
		t.Fatalf("an offline check is retried the same day: %q, %d requests", v, calls2.Load())
	}
}

// checkCodex: updates.check off, or no Codex agent: no request, no line; a
// Codex agent behind the registry: the line.
func TestCheckCodex(t *testing.T) {
	ctx := context.Background()
	agents := func(snaps map[string]bus.AgentSnapshot) func(context.Context) (map[string]bus.AgentSnapshot, error) {
		return func(context.Context) (map[string]bus.AgentSnapshot, error) { return snaps, nil }
	}
	with := map[string]bus.AgentSnapshot{"rev": codexAgent("0.170.0")}
	for _, c := range []struct {
		name  string
		on    bool
		snaps map[string]bus.AgentSnapshot
		line  bool
		calls int32
	}{
		{"off", false, with, false, 0},
		{"no codex agent", true, map[string]bus.AgentSnapshot{"coder": {Client: "claude-code"}}, false, 0},
		{"behind", true, with, true, 1},
	} {
		s, calls := npmRegistry(t, "0.171.0")
		got := checkCodex(ctx, c.on, agents(c.snaps), s.Client(), s.URL+"/@openai/codex/latest",
			filepath.Join(t.TempDir(), "c.json"), time.Now())
		if (got != "") != c.line || calls.Load() != c.calls {
			t.Errorf("%s: line %q after %d requests", c.name, got, calls.Load())
		}
	}
}

// The header carries the whole line, its install command included (spec 4,
// reviewer-codex), when it fits; the About page too, the command as the
// row's note.
func TestCodexLineInHeaderAndAbout(t *testing.T) {
	line := codexNotice("0.170.0", "0.171.0")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var text string
	for _, l := range headerLines(headerData{project: "demo", codex: line, now: now, health: health{ok: true, since: now}}, 200, TierWide) {
		for _, seg := range l {
			text += seg.Text
		}
		text += "\n"
	}
	if !strings.Contains(text, GlyphUp+" "+line) {
		t.Fatalf("header lacks the whole line with its command: %q", text)
	}
	v := withCodexRow(line, aboutVersions{Build: []aboutRow{{Label: "busmon"}}})
	last := v.Build[len(v.Build)-1]
	if last.Label != "codex" || last.Value != GlyphUp+" 0.171.0 available (installed 0.170.0)" || last.Note != "npm install -g @openai/codex@latest" ||
		len(v.Lines) != 1 || v.Lines[0] != line {
		t.Fatalf("about %+v", v)
	}
	if v := withCodexRow("", aboutVersions{Build: []aboutRow{{Label: "busmon"}}}); len(v.Build) != 1 || len(v.Lines) != 0 {
		t.Fatalf("no line, about %+v", v)
	}
}

// An error answer is no version, whatever its body says.
func TestLatestCodexIgnoresAnErrorAnswer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"version":"9.9.9"}`))
	}))
	defer s.Close()
	if v := latestCodex(context.Background(), s.Client(), s.URL, filepath.Join(t.TempDir(), "c.json"), time.Now()); v != "" {
		t.Fatalf("version %q from a 503", v)
	}
}

// foureyes on login-updates-2: what the registry or the cache says is drawn
// in the terminal, so only a plain version is kept: escape sequences, any
// other text and an oversized body give no version (and nothing is drawn).
func TestLatestCodexKeepsOnlyAVersion(t *testing.T) {
	for _, body := range []string{
		`{"version":"0.170.0-\u001b]52;c;aGk=\u0007"}`,
		`{"version":"0.171.0 run curl x|sh"}`,
		`{"version":"1.2"}`,
		`{"version":"0.171.0","pad":"` + strings.Repeat("x", 2<<20) + `"}`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if v := latestCodex(context.Background(), s.Client(), s.URL, filepath.Join(t.TempDir(), "c.json"), time.Now()); v != "" {
			t.Errorf("kept %q from %.60q", v, body)
		}
		s.Close()
	}
	s, _ := npmRegistry(t, "0.171.0-beta.1")
	if v := latestCodex(context.Background(), s.Client(), s.URL+"/@openai/codex/latest", filepath.Join(t.TempDir(), "c.json"), time.Now()); v != "0.171.0-beta.1" {
		t.Fatalf("a pre-release version refused: %q", v)
	}
	// A cache file written by hand is checked on read too.
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"checked_at":"`+time.Now().Format(time.RFC3339)+`","version":"0.1.0-\u001b[2J"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := latestCodex(context.Background(), s.Client(), s.URL+"/@openai/codex/latest", path, time.Now()); v != "" {
		t.Fatalf("kept %q from the cache", v)
	}
}

// The installed label is drawn too: a label that is no version is ignored.
func TestCodexInstalledKeepsOnlyAVersion(t *testing.T) {
	if got := codexInstalled(map[string]bus.AgentSnapshot{"rev": codexAgent("0.170.0\x1b[2J")}); got != "" {
		t.Fatalf("installed %q", got)
	}
}
