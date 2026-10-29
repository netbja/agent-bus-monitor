package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestBudgetTable(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	m := map[string]bus.BudgetSnapshot{
		"anthropic": {
			Provider:     "anthropic",
			SessionPct:   25,
			SessionReset: "2026-07-23T11:30:00Z",
			WeeklyPct:    44,
			WeeklyReset:  "2026-07-28T13:00:00Z",
			Extra:        map[string]float64{"weekly_opus_pct": 12},
			TS:           now.Add(-2 * time.Minute).UnixMilli(),
		},
	}
	got := budgetTable(m, now)
	for _, want := range []string{
		"anthropic", "session 25%", "(resets in 1h30m)",
		"weekly 44%", "(resets in 5d3h)", "weekly_opus 12%", "2m ago",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("budgetTable missing %q in:\n%s", want, got)
		}
	}
}

// A window whose reset is in the past makes its percentage meaningless; saying
// so beats printing a negative countdown, which reads as a bug.
func TestBudgetTableRolledOverWindow(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	m := map[string]bus.BudgetSnapshot{
		"anthropic": {SessionPct: 99, SessionReset: "2026-07-23T09:00:00Z", TS: now.UnixMilli()},
	}
	got := budgetTable(m, now)
	if !strings.Contains(got, "window rolled over") {
		t.Errorf("want a rolled-over marker, got:\n%s", got)
	}
	if strings.Contains(got, "resets in -") {
		t.Errorf("negative countdown leaked into output:\n%s", got)
	}
}

func TestBudgetTableSortedAndEmpty(t *testing.T) {
	now := time.Now()
	m := map[string]bus.BudgetSnapshot{
		"openai":    {WeeklyPct: 1, TS: now.UnixMilli()},
		"anthropic": {WeeklyPct: 2, TS: now.UnixMilli()},
	}
	got := budgetTable(m, now)
	if strings.Index(got, "anthropic") > strings.Index(got, "openai") {
		t.Errorf("providers not sorted:\n%s", got)
	}
	if budgetTable(map[string]bus.BudgetSnapshot{}, now) != "" {
		t.Error("empty budget map should render nothing")
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0 ctx"}, {"999", "999 ctx"}, {"42033", "42k ctx"}, {"608917", "608k ctx"},
	}
	ints := []int{0, 999, 42033, 608917}
	for i, c := range cases {
		if got := humanTokens(ints[i]); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", ints[i], got, c.want)
		}
	}
	if got := humanTokens(1_250_000); got != "1.2M ctx" {
		t.Errorf("humanTokens(1250000) = %q, want 1.2M ctx", got)
	}
}

// A missing source must produce a note, never a failure: refresh runs from the
// sentinel's cron wake, where a hard exit loses the whole caretaker pass.
func TestCollectBudgetsMissingSourceIsANote(t *testing.T) {
	got, notes := collectBudgets(filepath.Join(t.TempDir(), "absent.json"))
	if len(got) != 0 {
		t.Errorf("want no budgets, got %v", got)
	}
	if len(notes) == 0 {
		t.Error("a missing cache must produce a note")
	}
	if got, notes := collectBudgets(""); len(got) != 0 || len(notes) == 0 {
		t.Error("an empty path must produce a note, not a panic")
	}
}

func TestCollectBudgets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(p, []byte(`{"sessionUsage":25,"weeklyUsage":44}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, notes := collectBudgets(p)
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %v", notes)
	}
	if got["anthropic"].SessionPct != 25 {
		t.Errorf("got %+v", got["anthropic"])
	}
}

func writeAgentTranscript(t *testing.T, root, sessionID, body string) {
	t.Helper()
	dir := filepath.Join(root, "-data-projects-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectAgentUsage(t *testing.T) {
	root := t.TempDir()
	writeAgentTranscript(t, root, "sess-coder",
		`{"type":"assistant","timestamp":"2026-07-23T09:00:00.000Z","message":{"model":"claude-opus-4-8",`+
			`"usage":{"input_tokens":3,"cache_read_input_tokens":141000,"cache_creation_input_tokens":0,"output_tokens":9}}}`)

	agents := map[string]bus.AgentSnapshot{
		"coder": {State: "working", PID: 40, PIDStart: 1, IdentSession: "sess-coder"},
		"gpt":   {State: "idle"},                                                    // non-Claude peer: no session id
		"ghost": {State: "idle", PID: 41, PIDStart: 2, IdentSession: "sess-nope-1"}, // proved, transcript gone
	}
	got, notes := collectAgentUsage(agents, root)

	c, ok := got["coder"]
	if !ok {
		t.Fatalf("coder missing from %v (notes: %v)", got, notes)
	}
	if c.Model != "claude-opus-4-8" || c.Ctx != "141k ctx" {
		t.Errorf("coder = %+v, want model claude-opus-4-8 / 141k ctx", c)
	}
	if c.Source != "transcript" || c.Provider != "anthropic" {
		t.Errorf("coder provenance = %q/%q", c.Source, c.Provider)
	}
	// Account-scope fields stay empty: those belong to BudgetSnapshot now.
	if c.Weekly != "" || c.Session != "" || c.Reset != "" {
		t.Errorf("account-scope fields leaked into a per-agent snapshot: %+v", c)
	}
	if _, ok := got["gpt"]; ok {
		t.Error("an agent with no session id must be skipped, not invented")
	}
	if _, ok := got["ghost"]; ok {
		t.Error("an agent whose transcript is gone must be skipped")
	}
	if len(notes) != 2 {
		t.Errorf("want a note per skipped agent, got %v", notes)
	}
}

// usage-session: {p}:usage is read only from the session proved with the
// agent's process (IdentSession, with a pid), exactly like {p}:context. An
// unverified status that names another session (Session) never moves the
// transcript an agent's usage is read from.
func TestCollectAgentUsageReadsOnlyTheProvedSession(t *testing.T) {
	root := t.TempDir()
	turn := func(model string, read int) string {
		return `{"type":"assistant","timestamp":"2026-07-23T09:00:00.000Z","message":{"model":"` + model + `",` +
			`"usage":{"input_tokens":0,"cache_read_input_tokens":` + strconv.Itoa(read) + `,"cache_creation_input_tokens":0,"output_tokens":1}}}`
	}
	writeAgentTranscript(t, root, "sess-proved-1", turn("claude-proved", 50000))
	writeAgentTranscript(t, root, "sess-forged-1", turn("claude-forged", 90000))

	agents := map[string]bus.AgentSnapshot{
		// verified process; a later unverified status moved Session elsewhere
		"coder": {State: "working", PID: 42, PIDStart: 7, Session: "sess-forged-1", IdentSession: "sess-proved-1"},
		// only an unverified status ever named a session
		"loose": {State: "idle", Session: "sess-forged-1"},
		// a proved session but no pid: not verified now
		"nopid": {State: "idle", IdentSession: "sess-proved-1"},
		// a glob would match any transcript under root
		"glob": {State: "idle", PID: 43, PIDStart: 8, IdentSession: "*"},
	}
	got, notes := collectAgentUsage(agents, root)
	if c := got["coder"]; c.Model != "claude-proved" || c.Ctx != "50k ctx" {
		t.Errorf("coder = %+v, want the proved session's transcript (claude-proved, 50k ctx)", c)
	}
	for _, name := range []string{"loose", "nopid", "glob"} {
		if u, ok := got[name]; ok {
			t.Errorf("%s published %+v from a session never proved with its process", name, u)
		}
	}
	if len(notes) != 3 {
		t.Errorf("want a note per skipped agent, got %v", notes)
	}
}
