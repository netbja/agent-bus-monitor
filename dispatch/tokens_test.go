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
