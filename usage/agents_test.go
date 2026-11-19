package usage

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

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
	got, notes := CollectAgentUsage(agents, root)

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
	got, notes := CollectAgentUsage(agents, root)
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
