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
		{"background tasks, from Stop's list", claude, Facts{Hooks: &HookFacts{Tasks: 2}, Process: idleProc}, BackgroundRunning, "background task ×2"},
		{"the hooks say none", claude, Facts{Hooks: &HookFacts{}, Process: idleProc}, BackgroundNone, ""},
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

// background-work-visible step 2: the process source's count and oldest age,
// for the observation: active non-helper descendants only, no names.
func TestJudgeProcessCount(t *testing.T) {
	now := time.Unix(1000000, 0)
	claude := Background{Authoritative: []string{SourceHooks, SourceProcess}}
	f := Facts{Process: &ProcessFacts{Window: 10 * time.Second, Procs: []Proc{
		{Name: "make", Seen: now.Add(-14 * time.Minute), CPUGrowth: 5 * time.Second},
		{Name: "go", Seen: now.Add(-3 * time.Minute), CPUGrowth: time.Second},
		{Name: "node", Start: now.Add(-40 * time.Minute)}, // an idle MCP helper: not counted
	}}, ExtraHelpers: []string{"node"}}
	j := claude.Judge(f, now)
	if j.Procs() != 2 || j.OldestAge() != 14*time.Minute {
		t.Fatalf("procs %d oldest %v", j.Procs(), j.OldestAge())
	}
	if j.What() != "process 'make', process 'go'" { // a first sight never enters the text
		t.Fatalf("what %q", j.What())
	}
	if j := claude.Judge(Facts{Process: &ProcessFacts{Window: 10 * time.Second}}, now); j.Procs() != 0 || j.OldestAge() != 0 {
		t.Fatalf("no process: %d %v", j.Procs(), j.OldestAge())
	}
}

// background-work-visible §4b: the agent's declared text is a fourth source,
// always running; the judgement names the source that blocked; without the
// declaration (context escalation, for auto-clear) the judgement is the
// other sources' alone.
func TestJudgeDeclared(t *testing.T) {
	now := time.Unix(1000000, 0)
	claude := Background{Authoritative: []string{SourceHooks, SourceProcess}}
	text := "2 checks running"
	idle := &ProcessFacts{Window: 10 * time.Second}
	j := claude.Judge(Facts{Hooks: &HookFacts{}, Process: idle, Declared: &text}, now)
	if j.State != BackgroundRunning || j.Source() != SourceDeclared || j.What() != "declared: 2 checks running" {
		t.Fatalf("declared: %v %q %q", j.State, j.Source(), j.What())
	}
	if w := j.WithoutDeclared(); w.State != BackgroundNone || w.What() != "" {
		t.Fatalf("without the declaration, hooks and process say none: %v %q", w.State, w.What())
	}
	busy := &ProcessFacts{Window: 10 * time.Second, Procs: []Proc{{Name: "make", CPUGrowth: time.Second}}}
	j = claude.Judge(Facts{Hooks: &HookFacts{}, Process: busy, Declared: &text}, now)
	if w := j.WithoutDeclared(); w.State != BackgroundRunning || w.Source() != SourceProcess {
		t.Fatalf("observed work still skips: %v %q", w.State, w.Source())
	}
	if j := claude.Judge(Facts{Hooks: &HookFacts{Subagents: 1}, Process: idle}, now); j.Source() != SourceHooks {
		t.Fatalf("source %q", j.Source())
	}
	// Claude's Stop list alone (step 1's tasks, merged before this step).
	if j := claude.Judge(Facts{Hooks: &HookFacts{Tasks: 2}, Process: idle}, now); j.Source() != SourceHooks {
		t.Fatalf("tasks source %q", j.Source())
	}
	if j := claude.Judge(Facts{}, now); j.Source() != "unknown" {
		t.Fatalf("unknown source %q", j.Source())
	}
}

// DirectHelpers: a direct child that is not a shell is the client's helper,
// whatever its CPU; a direct shell and any grandchild still count; a profile
// without it counts them all (Codex: a one-command shell may exec in place).
func TestJudgeDirectHelpers(t *testing.T) {
	now := time.Unix(1000000, 0)
	procs := &ProcessFacts{Window: 10 * time.Second, Procs: []Proc{
		{Name: "codebase-memory", Direct: true, CPUGrowth: 9 * time.Second},
		{Name: "node", Direct: true, CPUGrowth: 9 * time.Second},
	}}
	claude := Background{Authoritative: []string{SourceHooks, SourceProcess}, DirectHelpers: true}
	if j := claude.Judge(Facts{Hooks: &HookFacts{}, Process: procs}, now); j.State != BackgroundNone || j.Procs() != 0 {
		t.Fatalf("the client's helpers: %v %d %q", j.State, j.Procs(), j.What())
	}
	work := &ProcessFacts{Window: 10 * time.Second, Procs: append(procs.Procs, Proc{Name: "bash", Direct: true}, Proc{Name: "node", CPUGrowth: time.Second})}
	if j := claude.Judge(Facts{Hooks: &HookFacts{}, Process: work}, now); j.State != BackgroundRunning || j.Procs() != 2 || j.What() != "process 'bash', process 'node'" {
		t.Fatalf("a command under its shell: %v %d %q", j.State, j.Procs(), j.What())
	}
	codex := Background{Authoritative: []string{SourceProcess}}
	if j := codex.Judge(Facts{Process: procs}, now); j.State != BackgroundRunning || j.Procs() != 2 {
		t.Fatalf("without DirectHelpers: %v %d", j.State, j.Procs())
	}
}

// The shipped Claude Code profile declares it; the others do not.
func TestProfilesDirectHelpers(t *testing.T) {
	for client, want := range map[string]bool{"claude": true, "codex": false, "grok": false, "kimi": false, "agy": false} {
		if got := Profiles()[client].Background.DirectHelpers; got != want {
			t.Errorf("%s: %v, want %v", client, got, want)
		}
	}
}
