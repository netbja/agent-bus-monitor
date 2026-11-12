package agentstate

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func boolp(b bool) *bool    { return &b }
func strp(s string) *string { return &s }

// Herdr exit step 2b, spec 4.3b: background work is none only when every
// source the client's profile declares authoritative says none; any source
// alone says running; a missing authoritative source is unknown (which the
// rule treats as running: fail closed).
func TestBackgroundJudgement(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	claude := Background{Authoritative: []string{SourceHooks, SourceProcess}, Helpers: compileAll(t, `^node$`)}
	codex := Background{Helpers: compileAll(t, `^codex$`), Screen: compileAll(t, `Background terminals`)}
	agy := Background{Authoritative: []string{SourceHooks}}
	idleProc := &ProcessFacts{Window: 10 * time.Second, Procs: []Proc{{Name: "node", Start: now.Add(-time.Hour), CPUGrowth: 10 * time.Millisecond}}}
	cases := []struct {
		name string
		prof Background
		f    Facts
		want BackgroundState
		what string
	}{
		{"every authoritative source says none", claude, Facts{Hooks: &HookFacts{}, Process: idleProc}, BackgroundNone, ""},
		{"a subagent", claude, Facts{Hooks: &HookFacts{Subagents: 2}, Process: idleProc}, BackgroundRunning, "subagent ×2"},
		{"a process that is no helper", claude, Facts{Hooks: &HookFacts{}, Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "node"}, {Name: "pytest", Start: now.Add(-4 * time.Minute)}}}}, BackgroundRunning, "process 'pytest' 4m"},
		{"an active helper", claude, Facts{Hooks: &HookFacts{}, Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "node", Start: now.Add(-90 * time.Second), CPUGrowth: 300 * time.Millisecond}}}}, BackgroundRunning, "process 'node' 1m"},
		{"a helper at the threshold is idle", claude, Facts{Hooks: &HookFacts{}, Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "node", CPUGrowth: 200 * time.Millisecond}}}}, BackgroundNone, ""},
		{"a launcher-configured MCP server is a helper", claude, Facts{Hooks: &HookFacts{}, Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "mcp-memory"}}}, ExtraHelpers: []string{"mcp-memory"}}, BackgroundNone, ""},
		{"an authoritative source not read", claude, Facts{Hooks: &HookFacts{}}, BackgroundUnknown, ""},
		{"nothing read", claude, Facts{}, BackgroundUnknown, ""},
		{"no authoritative source: never none", codex, Facts{Process: &ProcessFacts{Window: 10 * time.Second}, Screen: strp("› ready"), Hooks: &HookFacts{}}, BackgroundUnknown, ""},
		{"a non-authoritative source still says running", codex, Facts{Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "cargo", Start: now.Add(-30 * time.Second)}}}}, BackgroundRunning, "process 'cargo' 30s"},
		{"the screen says running", codex, Facts{Screen: strp("\x1b[1m2 Background terminals\x1b[0m running")}, BackgroundRunning, "background work on screen"},
		{"agy fullyIdle false", agy, Facts{Hooks: &HookFacts{FullyIdle: boolp(false)}}, BackgroundRunning, "background tasks"},
		{"agy fullyIdle true", agy, Facts{Hooks: &HookFacts{FullyIdle: boolp(true)}}, BackgroundNone, ""},
		{"several at once", claude, Facts{Hooks: &HookFacts{Subagents: 1}, Process: &ProcessFacts{Window: 10 * time.Second,
			Procs: []Proc{{Name: "pytest", Start: now.Add(-125 * time.Second)}}}}, BackgroundRunning, "subagent ×1, process 'pytest' 2m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.prof.Judge(c.f, now)
			if got.State != c.want || got.What() != c.what {
				t.Fatalf("got %v %q; want %v %q", got.State, got.What(), c.want, c.what)
			}
			if blocks := got.State != BackgroundNone; blocks != got.Blocks() {
				t.Fatalf("Blocks()=%v for %v", got.Blocks(), got.State)
			}
		})
	}
}

// Subagent counts: a start adds, a stop removes, never below zero, and a
// SessionStart resets, so a lost stop cannot hold the fact forever.
func TestSubagentCount(t *testing.T) {
	var h HookFacts
	for _, c := range []struct {
		event string
		want  int
	}{
		{"SubagentStart", 1}, {"SubagentStart", 2}, {"SubagentStop", 1},
		{"PostToolUse", 1}, {"SessionStart", 0}, {"SubagentStop", 0}, {"SubagentStart", 1},
	} {
		h = h.Apply(c.event)
		if h.Subagents != c.want {
			t.Fatalf("after %s: %d; want %d", c.event, h.Subagents, c.want)
		}
	}
	h.FullyIdle = boolp(false)
	if h = h.Apply("SessionStart"); h.FullyIdle != nil {
		t.Fatal("SessionStart kept agy's fullyIdle")
	}
}

// The profiles state their sources honestly: Claude Code's hooks and process
// tree; Codex's and Grok's process facts never authoritative (a shared
// daemon or leader process); the rest unverified, so never none yet.
func TestBackgroundProfiles(t *testing.T) {
	p := Profiles()
	want := map[string][]string{
		"claude": {SourceHooks, SourceProcess},
		"codex":  nil, "grok": nil, "kimi": nil, "agy": nil,
	}
	for c, w := range want {
		if got := p[c].Background.Authoritative; strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("%s authoritative %v; want %v", c, got, w)
		}
	}
	for _, c := range []string{"codex", "grok"} {
		for _, a := range p[c].Background.Authoritative {
			if a == SourceProcess {
				t.Errorf("%s: process facts authoritative", c)
			}
		}
	}
	for _, bad := range []string{
		"client = \"x\"\n[signature]\nprompt = '^>'\n[background]\nauthoritative = ['tea leaves']\n",
		"client = \"x\"\n[signature]\nprompt = '^>'\n[background]\nhelpers = ['(']\n",
		"client = \"x\"\n[signature]\nprompt = '^>'\nblocked = ['(']\n",
	} {
		if _, err := parseProfile("x.toml", []byte(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

func compileAll(t *testing.T, ps ...string) []*regexp.Regexp {
	t.Helper()
	var out []*regexp.Regexp
	for _, p := range ps {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}
