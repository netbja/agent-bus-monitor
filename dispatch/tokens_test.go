package dispatch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/tokens"
)

func transcriptLine(ts, msg string, in, out int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r-%s","message":{"id":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`,
		ts, msg, msg, in, out) + "\n"
}

// Only a session proved with the agent's process enters {p}:sessions; the
// pass counts the recorded sessions' transcripts (subagents included) per
// day, agent and provider, and a transcript no record names counts nowhere,
// whatever its directory.
func TestTokensPassCountsProvedSessionsOnly(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok-holder", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	root := t.TempDir()
	today := time.Now().UTC().Format("2006-01-02T") + "10:00:00Z"
	day := time.Now().UTC().Format("2006-01-02")
	write := func(rel, s string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("-data-projects-x/0198e5c4-coder-1111.jsonl", transcriptLine(today, "m1", 10, 1))
	write("-data-projects-x/0198e5c4-coder-1111/subagents/agent-a.jsonl", transcriptLine(today, "m2", 5, 1))
	write("-data-projects-x/0198e5c4-forged-222.jsonl", transcriptLine(today, "m3", 1000, 1000))
	write("-data-projects-x/0198e5c4-orphan-33.jsonl", transcriptLine(today, "m4", 2000, 2000))
	snaps := map[string]bus.AgentSnapshot{
		"coder":  {PID: 10, IdentSession: "0198e5c4-coder-1111", Provider: "anthropic", Client: "claude"},
		"forger": {Session: "0198e5c4-forged-222"},                        // unverified: never recorded
		"evil":   {PID: 11, IdentSession: "../-data-projects-x/0198e5c4"}, // not a session id
	}
	l := &loop{b: b, token: "tok-holder", cfg: Config{Log: io.Discard, Tokens: TokensConfig{Root: root, Every: time.Minute,
		Budget: tokens.Budget{Files: 64, Bytes: 1 << 20}, Location: time.UTC}}}
	if err := l.tokensPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	recs, _ := b.Sessions(ctx)
	if len(recs) != 1 || recs["0198e5c4-coder-1111"].Agent != "coder" {
		t.Fatalf("sessions %v, want only coder's proved one", recs)
	}
	rows, meta, ok, _ := b.Tokens(ctx)
	if !ok || meta.Partial {
		t.Fatalf("tokens ok=%v meta %+v", ok, meta)
	}
	want := bus.TokenRow{In: 15, Out: 2, N: 2}
	if len(rows) != 1 || rows[bus.TokenKey(day, "coder", "anthropic")] != want {
		t.Fatalf("rows %v, want coder's transcript and subagent only", rows)
	}
}

func codexLine(ts string, input, output int) string {
	u := fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d}`, input, output, input+output)
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s,"last_token_usage":%s,"model_context_window":272000}}}`, ts, u, u) + "\n"
}

// codex-token-attribution step 2: a proved Codex session (Client codex) is
// counted from its rollout, found by its id in its start-date folder, on the
// agent's rows under its provider; a Claude session in the same pass counts
// as before; a Codex rollout no proved session names counts nowhere.
func TestTokensPassCountsProvedCodexSessions(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok-holder", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	claude, codex := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	today, day := now.Format("2006-01-02T")+"10:00:00Z", now.Format("2006-01-02")
	write := func(root, rel, s string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	const id = "01a1a38b-a635-76d0-bbc6-348e3c47e603"
	write(claude, "-data-projects-x/0198e5c4-coder-1111.jsonl", transcriptLine(today, "m1", 10, 1))
	started := now.AddDate(0, 0, -5) // a session started days ago, still appended today
	write(codex, filepath.Join(started.Format("2006/01/02"), "rollout-"+started.Format("2006-01-02T15-04-05")+"-"+id+".jsonl"),
		`{"timestamp":"`+today+`","type":"session_meta","payload":{"id":"`+id+`"}}`+"\n"+codexLine(today, 300, 20))
	write(codex, filepath.Join(now.Format("2006/01/02"), "rollout-x-0198e5c4-stray-9.jsonl"), codexLine(today, 5000, 5000))
	// Each client's root is searched for its own sessions only: a rollout named
	// with the Claude session's id, a transcript named with the Codex one.
	write(codex, filepath.Join(now.Format("2006/01/02"), "rollout-x-0198e5c4-coder-1111.jsonl"), codexLine(today, 7000, 7000))
	write(claude, "-data-projects-x/"+id+".jsonl", transcriptLine(today, "m9", 9000, 9000))
	snaps := map[string]bus.AgentSnapshot{
		"coder":          {PID: 10, IdentSession: "0198e5c4-coder-1111", Provider: "anthropic", Client: "claude-code"},
		"reviewer-codex": {PID: 12, IdentSession: id, Provider: "openai", Client: "codex"},
	}
	l := &loop{b: b, token: "tok-holder", cfg: Config{Log: io.Discard, Tokens: TokensConfig{Root: claude, CodexRoot: codex, Every: time.Minute,
		Budget: tokens.Budget{Files: 64, Bytes: 1 << 20}, Location: time.UTC}}}
	if err := l.tokensPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	rows, meta, ok, _ := b.Tokens(ctx)
	if !ok || meta.Partial {
		t.Fatalf("tokens ok=%v meta %+v", ok, meta)
	}
	if got := rows[bus.TokenKey(day, "reviewer-codex", "openai")]; got != (bus.TokenRow{In: 300, Out: 20, N: 1}) {
		t.Errorf("codex row %+v (all %v)", got, rows)
	}
	if got := rows[bus.TokenKey(day, "coder", "anthropic")]; got != (bus.TokenRow{In: 10, Out: 1, N: 1}) {
		t.Errorf("claude row %+v", got)
	}
	if len(rows) != 2 {
		t.Errorf("rows %v: the stray rollout counted", rows)
	}
}

// With Codex's root alone (no Claude directory), the pass still counts the
// proved Codex sessions (reviewer-codex).
func TestTokensPassCodexRootAlone(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok-holder", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	codex := t.TempDir()
	now := time.Now().UTC()
	today, day := now.Format("2006-01-02T")+"10:00:00Z", now.Format("2006-01-02")
	const id = "01a1a38b-a635-76d0-bbc6-348e3c47e603"
	dir := filepath.Join(codex, now.Format("2006/01/02"))
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rollout-x-"+id+".jsonl"), []byte(codexLine(today, 300, 20)), 0o644)
	snaps := map[string]bus.AgentSnapshot{"reviewer-codex": {PID: 12, IdentSession: id, Provider: "openai", Client: "codex"}}
	l := &loop{b: b, token: "tok-holder", cfg: Config{Log: io.Discard, Tokens: TokensConfig{CodexRoot: codex, Every: time.Minute,
		Budget: tokens.Budget{Files: 64, Bytes: 1 << 20}, Location: time.UTC}}}
	if err := l.tokensPass(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	rows, _, ok, _ := b.Tokens(ctx)
	if !ok || rows[bus.TokenKey(day, "reviewer-codex", "openai")] != (bus.TokenRow{In: 300, Out: 20, N: 1}) {
		t.Fatalf("ok=%v rows %v", ok, rows)
	}
}

// No Claude directory: a Claude record never globs the working directory.
func TestTokensPassNoClaudeRootReadsNoRelativePath(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	now := time.Now().UTC()
	os.MkdirAll(filepath.Join(cwd, "-p"), 0o755)
	os.WriteFile(filepath.Join(cwd, "-p", "0198e5c4-coder-1111.jsonl"), []byte(transcriptLine(now.Format("2006-01-02T")+"10:00:00Z", "m1", 10, 1)), 0o644)
	recs := map[string]bus.SessionRecord{"0198e5c4-coder-1111": {Agent: "coder", Provider: "anthropic", Client: "claude-code"}}
	rows, _ := countSessions(tokens.NewClaudeScan(time.UTC), tokens.NewCodexScan(time.UTC), "", t.TempDir(), recs, tokens.Budget{Files: 64, Bytes: 1 << 20}, now, time.UTC)
	if len(rows) != 0 {
		t.Errorf("rows %v: a transcript under the working directory was read", rows)
	}
}

// Codex reads on the budget the Claude scan left: with room for one file,
// the Claude transcript takes it and the pass is partial.
func TestTokensPassCodexSharesTheBudget(t *testing.T) {
	claude, codex := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	today := now.Format("2006-01-02T") + "10:00:00Z"
	const id = "01a1a38b-a635-76d0-bbc6-348e3c47e603"
	os.MkdirAll(filepath.Join(claude, "-p"), 0o755)
	os.WriteFile(filepath.Join(claude, "-p", "0198e5c4-coder-1111.jsonl"), []byte(transcriptLine(today, "m1", 10, 1)), 0o644)
	dir := filepath.Join(codex, now.Format("2006/01/02"))
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rollout-x-"+id+".jsonl"), []byte(codexLine(today, 300, 20)), 0o644)
	recs := map[string]bus.SessionRecord{
		"0198e5c4-coder-1111": {Agent: "coder", Provider: "anthropic", Client: "claude-code"},
		id:                    {Agent: "reviewer-codex", Provider: "openai", Client: "codex"},
	}
	_, partial := countSessions(tokens.NewClaudeScan(time.UTC), tokens.NewCodexScan(time.UTC), claude, codex, recs, tokens.Budget{Files: 1, Bytes: 1 << 20}, now, time.UTC)
	if !partial {
		t.Error("Codex read past the budget the Claude scan used")
	}
}

// A first pass that cannot read everything within its budget publishes
// partial, and says so until every file has been read once.
func TestTokensPassIsPartialUntilComplete(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	b.AcquireDispatch(ctx, "tok-holder", time.Minute)
	root := t.TempDir()
	today := time.Now().UTC().Format("2006-01-02T") + "10:00:00Z"
	snaps := map[string]bus.AgentSnapshot{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("0198e5c4-agent-%04d", i)
		p := filepath.Join(root, "-x", id+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(transcriptLine(today, fmt.Sprint("m", i), 1, 1)), 0o644)
		snaps[fmt.Sprintf("a%d", i)] = bus.AgentSnapshot{PID: 10 + i, IdentSession: id}
	}
	l := &loop{b: b, token: "tok-holder", cfg: Config{Log: io.Discard, Tokens: TokensConfig{Root: root, Every: time.Nanosecond,
		Budget: tokens.Budget{Files: 2, Bytes: 1 << 20}, Location: time.UTC}}}
	l.tokensPass(ctx, snaps)
	if _, meta, _, _ := b.Tokens(ctx); !meta.Partial {
		t.Error("two files of three, yet not partial")
	}
	l.tokensPass(ctx, snaps)
	if _, meta, _, _ := b.Tokens(ctx); meta.Partial {
		t.Error("every file read, yet partial")
	}
}

// Without the lease the pass writes nothing; with no root it does nothing.
func TestTokensPassFencedAndOff(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	b.AcquireDispatch(ctx, "someone-else", time.Minute)
	l := &loop{b: b, token: "tok-holder", cfg: Config{Log: io.Discard, Tokens: TokensConfig{Root: t.TempDir(), Every: time.Minute}}}
	if err := l.tokensPass(ctx, map[string]bus.AgentSnapshot{"coder": {PID: 1, IdentSession: "0198e5c4-coder-1111"}}); err != bus.ErrNotHolder {
		t.Fatalf("without the lease: %v", err)
	}
	if n, _ := r.Exists(ctx, bus.SessionsKey(b.Project()), bus.TokensKey(b.Project())).Result(); n != 0 {
		t.Fatalf("%d keys written without the lease", n)
	}
	off := &loop{cfg: Config{Log: io.Discard}}
	if err := off.tokensPass(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

// A window is sampled when its reading changes, or every 15 minutes
// unchanged; a never-observed window is not.
func TestQuotaHistSamples(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 46, ObservedAt: 1}, {Name: "7d", UsedPct: 8, ObservedAt: 1}}},
		"moonshot":  {Windows: []bus.BudgetWindow{{Name: "7d"}}},
	}
	last := map[string]float64{"anthropic|5h": 46, "anthropic|7d": 7}
	at := map[string]time.Time{"anthropic|5h": now.Add(-time.Minute), "anthropic|7d": now.Add(-time.Minute)}
	got := quotaHistSamples(budgets, last, at, now)
	if len(got) != 1 || got[0].Window != "7d" || got[0].Pct != 8 {
		t.Fatalf("changed only: %+v", got)
	}
	at["anthropic|5h"] = now.Add(-quotaHistEvery)
	if got := quotaHistSamples(budgets, last, at, now); len(got) != 2 {
		t.Fatalf("15 minutes unchanged: %+v", got)
	}
}
