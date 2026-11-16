package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// recHost records every Start's spec on a fake host.
type recHost struct {
	*fake.Host
	specs []termhost.Spec
}

func (h *recHost) Start(ctx context.Context, s termhost.Spec) (termhost.Term, error) {
	h.specs = append(h.specs, s)
	return h.Host.Start(ctx, s)
}

// teamFixture: coder on claude (fallback claude-alt), reviewer-codex on
// codex, master; a template with busmon then the three agent tabs.
func teamFixture(t *testing.T) (*rolecfg.Manifest, upTemplate) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"claude\"\n" +
		"[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.claude-alt]\nclient = \"claude-code\"\nprovider = \"bedrock\"\nmodel = \"sonnet\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.claude-opus]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.codex-review]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"gpt\"\n" +
		"[roles.master]\ntier = \"boot\"\nskills = []\n" +
		"[roles.coder]\ntier = \"boot\"\nskills = []\nfallback_profiles = [\"claude-alt\"]\n" +
		"[roles.reviewer-codex]\ntier = \"boot\"\nskills = []\nprofile = \"codex-review\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	launch := func(role string) string {
		return "env ROLES_TOML=" + path + " /x/scripts/agent-launch " + role + " demo"
	}
	tpl := upTemplate{Name: "demo", Tabs: []upTab{
		{Name: "busmon", Command: "/x/busmon --project demo"},
		{Name: "master", Command: launch("master")},
		{Name: "coder", Command: launch("coder")},
		{Name: "reviewer-codex", Command: launch("reviewer-codex")},
	}}
	return m, tpl
}

func openFixture(t *testing.T, h *recHost, resumes map[string]bus.ResumeRecord) (string, []string) {
	t.Helper()
	m, tpl := teamFixture(t)
	var out bytes.Buffer
	var notices []string
	if _, err := openTeam(context.Background(), h, m, "demo", tpl.Tabs, resumes, nil, func(s string) { notices = append(notices, s) }, false, &out); err != nil {
		t.Fatal(err)
	}
	return out.String(), notices
}

// started is the roles started, in order.
func (h *recHost) started() []string {
	out := make([]string, len(h.specs))
	for i, s := range h.specs {
		out[i] = s.Name
	}
	return out
}

// startIndex is role's place among the starts, -1 when never started.
func startIndex(h *recHost, role string) int {
	for i, s := range h.specs {
		if s.Name == role {
			return i
		}
	}
	return -1
}

func specOf(h *recHost, role string) (termhost.Spec, bool) {
	for _, s := range h.specs {
		if s.Name == role {
			return s, true
		}
	}
	return termhost.Spec{}, false
}

// herdr exit §4.7: up starts each agent tab's role on the holder (busmon is
// the person's own window), resuming a recorded session through Start's
// resume_session only, and starts nothing a second time.
func TestOpenTeam(t *testing.T) {
	h := &recHost{Host: fake.New()}
	out, notices := openFixture(t, h, map[string]bus.ResumeRecord{"coder": {Session: "5f2b1c3d-aa"}})
	if len(h.specs) != 3 || len(notices) != 0 {
		t.Fatalf("specs %+v, notices %q", h.specs, notices)
	}
	if s, _ := specOf(h, "coder"); s.ResumeSession != "5f2b1c3d-aa" || s.Profile != "" || s.Project != "demo" {
		t.Fatalf("coder %+v", s)
	}
	if s, _ := specOf(h, "master"); s.ResumeSession != "" {
		t.Fatalf("master %+v: no record, fresh", s)
	}
	if !strings.Contains(out, "coder: started, resuming session 5f2b1c3d-aa") || !strings.Contains(out, "busmon --project demo") {
		t.Fatalf("out %q", out)
	}
	m, tpl := teamFixture(t)
	var again bytes.Buffer
	if _, err := openTeam(context.Background(), h, m, "demo", tpl.Tabs, nil, nil, func(string) {}, false, &again); err != nil {
		t.Fatal(err)
	}
	if len(h.specs) != 3 || !strings.Contains(again.String(), "coder: running") {
		t.Fatalf("a second up started %+v: %q", h.specs, again.String())
	}
}

// §4.7's record conditions, checked before Start: an invalid session, a
// profile outside the role's own and its fallbacks, a session of another
// client: up starts fresh and tells the person; a fallback resumes on it.
func TestOpenTeamRecordConditions(t *testing.T) {
	for _, c := range []struct {
		name    string
		role    string
		rec     bus.ResumeRecord
		session string
		profile string
		notice  string
	}{
		{"an injected session", "coder", bus.ResumeRecord{Session: "5f2b1c3d-aa;rm"}, "", "", "coder: its resume record is not used"},
		{"a profile outside the set", "reviewer-codex", bus.ResumeRecord{Session: "5f2b1c3d-aa", Profile: "claude-opus"}, "", "", "claude-opus is neither"},
		{"a fallback resumes on it", "coder", bus.ResumeRecord{Session: "5f2b1c3d-aa", Profile: "claude-alt"}, "5f2b1c3d-aa", "claude-alt", ""},
		{"a client without a verified resume", "reviewer-codex", bus.ResumeRecord{Session: "5f2b1c3d-aa"}, "", "", "has no verified resume"},
	} {
		h := &recHost{Host: fake.New()}
		_, notices := openFixture(t, h, map[string]bus.ResumeRecord{c.role: c.rec})
		s, ok := specOf(h, c.role)
		if !ok || s.ResumeSession != c.session || s.Profile != c.profile {
			t.Errorf("%s: started %+v", c.name, s)
		}
		if got := strings.Join(notices, "\n"); c.notice == "" && got != "" || c.notice != "" && !strings.Contains(got, c.notice) {
			t.Errorf("%s: notices %q", c.name, got)
		}
	}
}

// A role under a swap or a self-repair is left to that operation; an
// intent that cannot be read is said, and the role started.
func TestOpenTeamSkipsARoleUnderSelfRepair(t *testing.T) {
	m, tpl := teamFixture(t)
	h := &recHost{Host: fake.New()}
	var out bytes.Buffer
	intent := func(role string) (bus.SwapIntent, bool, error) {
		switch role {
		case "master":
			return bus.SwapIntent{Kind: bus.SwapKindSelfRepair, Nonce: "n1"}, true, nil
		case "coder":
			return bus.SwapIntent{}, false, errors.New("connection refused")
		}
		return bus.SwapIntent{}, false, nil
	}
	if _, err := openTeam(context.Background(), h, m, "demo", tpl.Tabs, nil, intent, func(string) {}, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := specOf(h, "master"); ok || !strings.Contains(out.String(), "master: skipped (self-repair in progress)") {
		t.Fatalf("master started under a self-repair: %q", out.String())
	}
	if _, ok := specOf(h, "coder"); !ok || !strings.Contains(out.String(), "coder: intent unreadable (connection refused)") {
		t.Fatalf("coder: %q", out.String())
	}
}
