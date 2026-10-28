package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/tokens"
)

type fakeTokens struct {
	rows     map[string]bus.TokenRow
	meta     bus.TokensMeta
	ok       bool
	budgets  map[string]bus.BudgetSnapshot
	agents   map[string]bus.AgentSnapshot
	sessions map[string]bus.SessionRecord
}

func (f fakeTokens) Tokens(context.Context) (map[string]bus.TokenRow, bus.TokensMeta, bool, error) {
	return f.rows, f.meta, f.ok, nil
}
func (f fakeTokens) Budgets(context.Context) (map[string]bus.BudgetSnapshot, error) {
	return f.budgets, nil
}
func (f fakeTokens) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	return f.agents, nil
}
func (f fakeTokens) Sessions(context.Context) (map[string]bus.SessionRecord, error) {
	return f.sessions, nil
}

var tokNow = time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

func tokDeps(projects map[string]fakeTokens) tokensDeps {
	return tokensDeps{
		Projects: func(context.Context) ([]string, error) {
			var out []string
			for n := range projects {
				out = append(out, n)
			}
			return out, nil
		},
		Open: func(p string) (tokensReader, error) {
			f, ok := projects[p]
			if !ok {
				return nil, fmt.Errorf("no project %s", p)
			}
			return f, nil
		},
		Now: func() time.Time { return tokNow }, Loc: time.UTC,
	}
}

func tokFixture() map[string]fakeTokens {
	return map[string]fakeTokens{
		"agent-bus-monitor": {ok: true, rows: map[string]bus.TokenRow{
			"2026-10-28|foureyes|anthropic": {In: 1_000_000, Write: 200_000, Read: 500_000_000, Out: 206_000, N: 40},
			"2026-10-28|coder|anthropic":    {In: 300_000, Write: 45_000, Out: 83_000, N: 20},
			"2026-10-27|coder|anthropic":    {In: 2_000_000, Out: 500_000, N: 10},
		}, budgets: map[string]bus.BudgetSnapshot{"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 46, ObservedAt: 1, ResetsAt: tokNow.Add(2*time.Hour + 49*time.Minute).Unix()},
			{Name: "7d"}}}}, // 7d never observed: ?
			agents: map[string]bus.AgentSnapshot{
				"coder":          {PID: 1, IdentSession: "0198e5c4-coder-1", Provider: "anthropic"},
				"foureyes":       {PID: 2, IdentSession: "0198e5c4-foureyes-2", Provider: "anthropic"},
				"evil":           {PID: 6, IdentSession: "../x", Provider: "anthropic"}, // an id no path can carry: unknown
				"reviewer-codex": {PID: 3, Provider: "openai", Client: "codex"},
				"ui-kimi":        {PID: 4, Provider: "moonshot", Client: "kimi"},
				"sentinel":       {PID: 5, IdentSession: "0198e5c4-sentinel-5", Provider: "anthropic"},
			}},
		"hyperliquid": {ok: true, meta: bus.TokensMeta{Partial: true}, rows: map[string]bus.TokenRow{"2026-10-22|coder|anthropic": {In: 170_000, N: 1}}},
		"polymarket":  {},
	}
}

// Without a project, every project side by side, the busiest today first;
// a project with no tokens key folds into one "no data" line; a partial
// project's numbers carry ~.
func TestTokensAllProjects(t *testing.T) {
	var out, errw bytes.Buffer
	if code := runTokens(nil, "", tokDeps(tokFixture()), &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	s := out.String()
	for _, want := range []string{"account   anthropic 5h 46% (resets 2h49) · 7d ?", "PROJECT", "agent-bus-monitor", "1.5M", "289k",
		"polymarket   no data", "~0", "agentbus tokens --unattributed"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "agent-bus-monitor") > strings.Index(s, "hyperliquid") {
		t.Error("the busiest project is not first")
	}
}

// By agent: tokens today; ? with the reason for an agent whose client gives
// no counted session; 0 only for a proved session with no tokens today.
func TestTokensByAgent(t *testing.T) {
	var out bytes.Buffer
	runTokens([]string{"--by", "agent"}, "agent-bus-monitor", tokDeps(tokFixture()), &out, &out)
	s := out.String()
	for _, want := range []string{"foureyes", "1.2M", "206k", "reviewer-codex", "Codex session not proved", "ui-kimi", "Kimi logs give no token counts"} {
		if !strings.Contains(s, want) {
			t.Errorf("by agent lacks %q:\n%s", want, s)
		}
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "reviewer-codex") && strings.Contains(l, " 0 ") {
			t.Errorf("an unknown printed as 0: %q", l)
		}
		if strings.HasPrefix(l, "sentinel") && !strings.Contains(l, " 0 ") {
			t.Errorf("a proved session with nothing today is 0: %q", l)
		}
		if strings.HasPrefix(l, "evil") && (strings.Contains(l, " 0 ") || !strings.Contains(l, "?")) {
			t.Errorf("an invalid session id reads 0: %q", l)
		}
	}
	var js bytes.Buffer
	runTokens([]string{"--by", "agent", "--json"}, "agent-bus-monitor", tokDeps(tokFixture()), &js, &js)
	var got struct {
		Agents []tokens.AgentRow `json:"agents"`
	}
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, a := range got.Agents {
		if a.Agent == "reviewer-codex" && (a.In != nil || a.Reason == "") {
			t.Errorf("json: unknown with no reason: %+v", a)
		}
	}
}

// By day, and the argument rules.
func TestTokensByDayAndArgs(t *testing.T) {
	var out bytes.Buffer
	runTokens([]string{"--project", "agent-bus-monitor", "--by", "day", "--days", "2"}, "", tokDeps(tokFixture()), &out, &out)
	if s := out.String(); !strings.Contains(s, "2026-10-27") || !strings.Contains(s, "2026-10-28") || strings.Contains(s, "2026-10-26") {
		t.Errorf("by day:\n%s", s)
	}
	for _, args := range [][]string{{"--by", "agent"}, {"--days", "9"}, {"--by", "week", "--project", "x"}, {"--nope"}, {"--project", "Bad*"}} {
		var e bytes.Buffer
		if code := runTokens(args, "", tokDeps(tokFixture()), &e, &e); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// --unattributed: the host's transcripts no project's record names, counted
// once whatever directory holds them, plus Codex rollouts; a claimed session
// and its subagents are left out.
func TestTokensUnattributed(t *testing.T) {
	root := t.TempDir()
	claude, codex := filepath.Join(root, "claude"), filepath.Join(root, "codex")
	line := func(msg string, in, out int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-10-28T10:00:00Z","requestId":"r%s","message":{"id":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`, msg, msg, in, out) + "\n"
	}
	put := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	put(filepath.Join(claude, "-a", "claimed-session-1.jsonl"), line("m1", 9000, 9000))
	put(filepath.Join(claude, "-a", "claimed-session-1", "subagents", "x.jsonl"), line("m2", 9000, 9000))
	put(filepath.Join(claude, "-a", "orphan-session-2.jsonl"), line("m3", 2000, 300))
	put(filepath.Join(claude, "-b", "orphan-session-2.jsonl"), line("m3", 2000, 300)) // the same session in another directory
	put(filepath.Join(claude, "-a", "orphan-session-2", "subagents", "s.jsonl"), line("m5", 40, 4))
	put(filepath.Join(claude, "-b", "orphan-session-2", "subagents", "s.jsonl"), line("m5", 40, 4)) // its subagent, mirrored
	put(filepath.Join(codex, "2026", "10", "28", "rollout-2026-10-28T09-00-00-c.jsonl"),
		`{"timestamp":"2026-10-28T09:00:00Z","type":"session_meta","payload":{"id":"c"}}`+"\n"+
			`{"timestamp":"2026-10-28T09:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":0,"output_tokens":50,"total_tokens":550},"last_token_usage":{"input_tokens":500,"cached_input_tokens":0,"output_tokens":50,"total_tokens":550}}}}`+"\n")
	f := tokFixture()
	abm := f["agent-bus-monitor"]
	abm.sessions = map[string]bus.SessionRecord{"claimed-session-1": {Agent: "coder"}}
	f["agent-bus-monitor"] = abm
	d := tokDeps(f)
	d.ClaudeRoot, d.CodexRoot = claude, codex
	// "now" is the fixture's day; the files must be modified within 8 days of it
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			os.Chtimes(p, d.Now(), d.Now())
		}
		return nil
	})
	var out bytes.Buffer
	if code := runTokens([]string{"--unattributed", "--json"}, "", d, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var got struct {
		Providers map[string]struct{ In, Out int64 } `json:"providers"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if a := got.Providers["anthropic"]; a.In != 2040 || a.Out != 304 {
		t.Errorf("anthropic %+v, want the orphan and its subagent once, the claimed session and its subagent out", a)
	}
	if o := got.Providers["openai"]; o.In != 500 || o.Out != 50 {
		t.Errorf("openai %+v", o)
	}
}

// One host-scan budget for both providers: what Claude spends, Codex lacks;
// running out keeps the result partial.
func TestTokensUnattributedSharesOneBudget(t *testing.T) {
	root := t.TempDir()
	claude, codex := filepath.Join(root, "claude"), filepath.Join(root, "codex")
	put := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	line := `{"type":"assistant","timestamp":"2026-10-28T10:00:00Z","requestId":"r","message":{"id":"m","usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}` + "\n"
	put(filepath.Join(claude, "-a", "orphan-session-9.jsonl"), line)
	put(filepath.Join(codex, "2026", "10", "28", "rollout-2026-10-28T09-00-00-c.jsonl"), `{"timestamp":"2026-10-28T09:00:00Z","type":"session_meta","payload":{"id":"c"}}`+"\n")
	old := unattributedBudget
	t.Cleanup(func() { unattributedBudget = old })
	unattributedBudget.Bytes = int64(len(line)) // Claude's file uses it all
	d := tokDeps(tokFixture())
	d.ClaudeRoot, d.CodexRoot = claude, codex
	d.Now = func() time.Time { return tokNow }
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			os.Chtimes(p, tokNow, tokNow)
		}
		return nil
	})
	var out bytes.Buffer
	runTokens([]string{"--unattributed", "--json"}, "", d, &out, &out)
	var got struct{ Partial bool }
	json.Unmarshal(out.Bytes(), &got)
	if !got.Partial {
		t.Errorf("the budget ran out before Codex, yet complete: %s", out.String())
	}
}
