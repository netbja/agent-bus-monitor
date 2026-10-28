package dispatch

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// The tokens pass (spec docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md):
// it records every agent's proved session in {p}:sessions, counts the Claude
// Code transcripts of the recorded sessions incrementally (tokens.ClaudeScan,
// offsets in memory only) and replaces {p}:tokens with the sums. Only a
// session proved with the agent's process (IdentSession) is recorded: a
// status that merely names a session never points the pass at a file.
// Codex has no proved session yet: its tokens are only the person's
// `agentbus tokens --unattributed`.

// TokensConfig is the tokens pass's policy; an empty Root turns it off.
type TokensConfig struct {
	Root     string         // Claude Code's projects directory (~/.claude/projects)
	Every    time.Duration  // how often {p}:tokens is replaced (sessions are recorded each minute)
	Budget   tokens.Budget  // per pass: files with new bytes, bytes read
	Location *time.Location // the host's local days (nil = time.Local)
}

// tokensDays is how far back the tokens count: the record's 8 days.
const tokensDays = 8

// tokensState is the pass's memory: the scan and its throttles.
type tokensState struct {
	scan       *tokens.ClaudeScan
	sessionsAt time.Time
	tokensAt   time.Time
	complete   bool               // every file read once since the start
	histPct    map[string]float64 // provider|window -> the last published pct
	histAt     map[string]time.Time
}

// tokensPass records the proved sessions each minute and replaces
// {p}:tokens when due; a lost lease is ErrNotHolder.
func (l *loop) tokensPass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	c := l.cfg.Tokens
	if c.Root == "" {
		return nil
	}
	s := &l.tokens
	if s.sessionsAt.IsZero() || time.Since(s.sessionsAt) >= time.Minute {
		if err := l.b.FencedRecordSessions(ctx, l.token, provedSessions(snaps)); err != nil {
			return err
		}
		s.sessionsAt = time.Now()
	}
	if !s.tokensAt.IsZero() && time.Since(s.tokensAt) < c.Every {
		return nil
	}
	loc := c.Location
	if loc == nil {
		loc = time.Local
	}
	if s.scan == nil {
		s.scan = tokens.NewClaudeScan(loc)
	}
	recs, err := l.b.Sessions(ctx)
	if err != nil {
		return err
	}
	rows, partial := countSessions(s.scan, c.Root, recs, c.Budget, time.Now(), loc)
	if !partial {
		s.complete = true
	}
	if err := l.b.FencedPutTokens(ctx, l.token, rows, partial || !s.complete); err != nil {
		return err
	}
	s.tokensAt = time.Now()
	return nil
}

// provedSessions are the sessions proved with an agent's process.
func provedSessions(snaps map[string]bus.AgentSnapshot) map[string]bus.SessionRecord {
	out := map[string]bus.SessionRecord{}
	for name, s := range snaps {
		if s.PID == 0 || !tokens.ValidSessionID(s.IdentSession) {
			continue
		}
		out[s.IdentSession] = bus.SessionRecord{Agent: name, Provider: s.Provider, Client: s.Client}
	}
	return out
}

// countSessions scans the recorded sessions' files modified within the
// record's days, newest first, and sums them per day, agent and provider.
func countSessions(scan *tokens.ClaudeScan, root string, recs map[string]bus.SessionRecord, b tokens.Budget, now time.Time, loc *time.Location) (map[string]bus.TokenRow, bool) {
	type file struct {
		path, session string
		mod           time.Time
	}
	cutoff := now.Add(-tokensDays * 24 * time.Hour)
	var files []file
	for id := range recs {
		paths, err := tokens.SessionFiles(root, id) // the id is validated before any path is built
		if err != nil {
			continue
		}
		for _, p := range paths {
			if info, err := os.Stat(p); err == nil && info.ModTime().After(cutoff) {
				files = append(files, file{p, id, info.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	partial := scan.Pass(paths, b)
	// Only the files passed are summed: a file the scan still remembers but
	// that left the window (or the record) counts nowhere, even when a Codex-
	// style rebuild would have reset it.
	firstDay := now.In(loc).AddDate(0, 0, -tokensDays).Format("2006-01-02")
	rows := map[string]bus.TokenRow{}
	for _, f := range files {
		rec := recs[f.session]
		for day, u := range scan.Sums(f.path) {
			if day <= firstDay {
				continue
			}
			k := bus.TokenKey(day, rec.Agent, rec.Provider)
			r := rows[k]
			r.In, r.Write, r.Read, r.Out, r.N = r.In+u.In, r.Write+u.Write, r.Read+u.Read, r.Out+u.Out, r.N+u.N
			rows[k] = r
		}
	}
	return rows, partial
}

// quotaHistEvery is how often an unchanged window's reading is sampled anyway.
const quotaHistEvery = 15 * time.Minute

// quotaHistSamples are the windows whose reading changed since the last
// sample, or was last sampled quotaHistEvery ago; unknown windows (never
// observed) are not sampled.
func quotaHistSamples(budgets map[string]bus.BudgetSnapshot, lastPct map[string]float64, lastAt map[string]time.Time, now time.Time) []bus.QuotaSample {
	var out []bus.QuotaSample
	providers := make([]string, 0, len(budgets))
	for p := range budgets {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, p := range providers {
		for _, w := range budgets[p].Windows {
			if w.ObservedAt == 0 && w.UsedPct == 0 {
				continue
			}
			k := p + "|" + w.Name
			pct, seen := lastPct[k]
			if seen && pct == w.UsedPct && now.Sub(lastAt[k]) < quotaHistEvery {
				continue
			}
			out = append(out, bus.QuotaSample{Provider: p, Window: w.Name, Pct: w.UsedPct})
		}
	}
	return out
}

// quotaHist appends the due samples of the budgets just published.
func (l *loop) quotaHist(ctx context.Context, budgets map[string]bus.BudgetSnapshot) error {
	s := &l.tokens
	if s.histPct == nil {
		s.histPct, s.histAt = map[string]float64{}, map[string]time.Time{}
	}
	now := time.Now()
	samples := quotaHistSamples(budgets, s.histPct, s.histAt, now)
	if err := l.b.FencedAddQuotaHist(ctx, l.token, samples); err != nil {
		return err
	}
	for _, x := range samples {
		k := x.Provider + "|" + x.Window
		s.histPct[k], s.histAt[k] = x.Pct, now
	}
	return nil
}
