package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// agentbus tokens (spec docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md):
// read-only. Every project side by side by default (the account window is
// shared: which project eats it?), one project by agent or by day, or the
// remainder no project's record names (--unattributed: the person's own host
// scan, never a dispatcher's total). No cost, by rule; an unknown reads `?`,
// never 0.

// tokensReader is what one project's reads need (a *bus.Bus).
type tokensReader interface {
	Tokens(ctx context.Context) (map[string]bus.TokenRow, bus.TokensMeta, bool, error)
	Budgets(ctx context.Context) (map[string]bus.BudgetSnapshot, error)
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Sessions(ctx context.Context) (map[string]bus.SessionRecord, error)
}

type tokensDeps struct {
	Projects   func(ctx context.Context) ([]string, error)
	Open       func(project string) (tokensReader, error)
	Now        func() time.Time
	Loc        *time.Location
	ClaudeRoot string // ~/.claude/projects, for --unattributed
	CodexRoot  string // $CODEX_HOME/sessions, for --unattributed
}

func realTokensDeps(host string) tokensDeps {
	home, _ := os.UserHomeDir()
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return tokensDeps{
		Projects: func(ctx context.Context) ([]string, error) {
			c, err := bus.Connect(host)
			if err != nil {
				return nil, err
			}
			ps, err := bus.Projects(ctx, c)
			if err != nil {
				return nil, err
			}
			out := make([]string, len(ps))
			for i, p := range ps {
				out[i] = p.Project
			}
			return out, nil
		},
		Open: func(project string) (tokensReader, error) {
			c, err := bus.Connect(host)
			if err != nil {
				return nil, err
			}
			return bus.Open(c, project)
		},
		Now: time.Now, Loc: time.Local, ClaudeRoot: filepath.Join(claude, "projects"), CodexRoot: filepath.Join(codex, "sessions"),
	}
}

type tokensOpts struct {
	project      string
	days         int
	by           string // "", "agent", "day"
	json         bool
	unattributed bool
}

func parseTokensArgs(args []string, project string) (tokensOpts, error) {
	o := tokensOpts{project: project, days: 7}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--project":
			o.project, err = next()
		case "--all":
			o.project = ""
		case "--days":
			var v string
			if v, err = next(); err == nil {
				o.days, err = strconv.Atoi(v)
				if err == nil && (o.days < 1 || o.days > 8) {
					err = fmt.Errorf("--days is 1 to 8 (the record keeps 8 days)")
				}
			}
		case "--by":
			o.by, err = next()
			if err == nil && o.by != "agent" && o.by != "day" {
				err = fmt.Errorf("--by is agent or day")
			}
		case "--json":
			o.json = true
		case "--unattributed":
			o.unattributed = true
		default:
			err = fmt.Errorf("unknown argument %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	if o.project != "" && !bus.ValidName(o.project) {
		return o, fmt.Errorf("invalid project %q", o.project)
	}
	if o.by != "" && o.project == "" {
		return o, fmt.Errorf("--by needs --project")
	}
	return o, nil
}

// runTokens: project is the one given on the command line (never the
// ambient AGENT_BUS_PROJECT: without one, every project is shown).
func runTokens(args []string, project string, d tokensDeps, stdout, stderr io.Writer) int {
	o, err := parseTokensArgs(args, project)
	if err != nil {
		fmt.Fprintln(stderr, "agentbus tokens: "+err.Error())
		fmt.Fprintln(stderr, "usage: agentbus tokens [--project <p> | --all] [--days N] [--by agent|day] [--json] [--unattributed]")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch {
	case o.unattributed:
		return tokensUnattributed(ctx, o, d, stdout, stderr)
	case o.by != "":
		return tokensOneProject(ctx, o, d, stdout, stderr)
	}
	return tokensAll(ctx, o, d, stdout, stderr)
}

// tokProject is one project's reading.
type tokProject struct {
	Project string                  `json:"project"`
	Known   bool                    `json:"known"` // {p}:tokens exists
	Partial bool                    `json:"partial"`
	At      int64                   `json:"observed_at,omitempty"`
	Days    map[string]bus.TokenRow `json:"days,omitempty"` // day → the project's sum
	Reason  string                  `json:"reason,omitempty"`
	budgets map[string]bus.BudgetSnapshot
	rows    map[string]bus.TokenRow
	agents  map[string]bus.AgentSnapshot
}

func readProject(ctx context.Context, d tokensDeps, project string) tokProject {
	p := tokProject{Project: project}
	r, err := d.Open(project)
	if err != nil {
		p.Reason = err.Error()
		return p
	}
	rows, meta, ok, err := r.Tokens(ctx)
	switch {
	case err != nil:
		p.Reason = "read failed: " + err.Error()
	case !ok:
		p.Reason = "no tokens recorded (no dispatch service counting them)"
	default:
		p.Known, p.Partial, p.At, p.rows = true, meta.Partial, meta.At, rows
		p.Days = tokens.ProjectDays(rows)
	}
	p.budgets, _ = r.Budgets(ctx)
	p.agents, _ = r.Agents(ctx)
	return p
}

func tokensAll(ctx context.Context, o tokensOpts, d tokensDeps, stdout, stderr io.Writer) int {
	var names []string
	if o.project != "" {
		names = []string{o.project}
	} else {
		var err error
		if names, err = d.Projects(ctx); err != nil {
			fmt.Fprintln(stderr, "agentbus tokens: "+err.Error())
			return 1
		}
	}
	days := tokens.LastDays(d.Now(), d.Loc, o.days)
	today := days[len(days)-1]
	var ps []tokProject
	for _, n := range names {
		ps = append(ps, readProject(ctx, d, n))
	}
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i].Days[today], ps[j].Days[today]
		return a.In+a.Write+a.Out > b.In+b.Write+b.Out
	})
	if o.json {
		return tokJSON(stdout, map[string]any{"days": days, "projects": ps})
	}
	if acc := accountLine(ps, d.Now()); acc != "" {
		fmt.Fprintln(stdout, "account   "+acc)
		fmt.Fprintln(stdout)
	}
	max := int64(0)
	for _, p := range ps {
		for _, day := range days {
			u := p.Days[day]
			max = tokMaxI(max, u.In+u.Write+u.Out)
		}
	}
	fmt.Fprintf(stdout, "%-20s %9s %10s %11s  %-*s  %9s\n", "PROJECT", "TODAY IN", "TODAY OUT", "CACHE READ", o.days, fmt.Sprintf("%dD", o.days), fmt.Sprintf("%dD TOTAL", o.days))
	var none []string
	for _, p := range ps {
		if !p.Known {
			none = append(none, p.Project)
			continue
		}
		u := p.Days[today]
		var total int64
		bars := ""
		for _, day := range days {
			v := p.Days[day]
			total += v.In + v.Write + v.Out
			bars += tokSpark(v.In+v.Write+v.Out, max)
		}
		mark := ""
		if p.Partial {
			mark = "~" // still counting since its dispatcher started
		}
		fmt.Fprintf(stdout, "%-20s %9s %10s %11s  %s  %9s\n", tokTrunc(p.Project, 20), mark+tokCount(u.In+u.Write), mark+tokCount(u.Out),
			mark+tokCount(u.Read), bars, mark+tokCount(total))
	}
	if len(none) > 0 {
		fmt.Fprintf(stdout, "%s   no data (no dispatch service counting tokens)\n", strings.Join(none, " "))
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "tokens = input + cache writes (IN) and output (OUT); cache reads apart; ~ still counting; no cost, by rule.")
	fmt.Fprintln(stdout, "not attributed to any project: agentbus tokens --unattributed")
	return 0
}

// accountLine is the account windows, from the first project that has them
// (every project's dispatcher publishes the same account readings).
func accountLine(ps []tokProject, now time.Time) string {
	for _, p := range ps {
		if len(p.budgets) == 0 {
			continue
		}
		var provs []string
		for n := range p.budgets {
			provs = append(provs, n)
		}
		sort.Strings(provs)
		var parts []string
		for _, n := range provs {
			b := p.budgets[n]
			var ws []string
			for _, w := range b.Windows {
				s := fmt.Sprintf("%s %.0f%%", w.Name, w.UsedPct)
				if w.ResetsAt > 0 && w.UsedPct >= 1 {
					if left := time.Unix(w.ResetsAt, 0).Sub(now); left > 0 {
						s += " (resets " + tokDur(left) + ")"
					}
				}
				ws = append(ws, s)
			}
			if len(ws) == 0 {
				ws = []string{"?"}
			}
			parts = append(parts, n+" "+strings.Join(ws, " · "))
		}
		return strings.Join(parts, "   ")
	}
	return ""
}

func tokensOneProject(ctx context.Context, o tokensOpts, d tokensDeps, stdout, stderr io.Writer) int {
	p := readProject(ctx, d, o.project)
	days := tokens.LastDays(d.Now(), d.Loc, o.days)
	today := days[len(days)-1]
	if o.by == "day" {
		if o.json {
			return tokJSON(stdout, p)
		}
		if !p.Known {
			fmt.Fprintf(stdout, "%s: ? (%s)\n", o.project, p.Reason)
			return 0
		}
		fmt.Fprintf(stdout, "%-10s %9s %9s %11s %8s\n", "DAY", "IN", "OUT", "CACHE READ", "REQUESTS")
		for _, day := range days {
			u := p.Days[day]
			fmt.Fprintf(stdout, "%-10s %9s %9s %11s %8d\n", day, tokCount(u.In+u.Write), tokCount(u.Out), tokCount(u.Read), u.N)
		}
		return 0
	}
	rows := tokens.AgentRows(p.rows, p.Known, p.Reason, p.agents, today)
	if o.json {
		return tokJSON(stdout, map[string]any{"project": o.project, "partial": p.Partial, "observed_at": p.At, "agents": rows, "reason": p.Reason})
	}
	fmt.Fprintf(stdout, "%-16s %-10s %9s %10s  %s\n", "AGENT", "PROVIDER", "TODAY IN", "TODAY OUT", "NOTE")
	for _, r := range rows {
		in, out := "?", "?"
		if r.In != nil {
			in, out = tokCount(*r.In), tokCount(*r.Out)
		}
		fmt.Fprintf(stdout, "%-16s %-10s %9s %10s  %s\n", tokTrunc(r.Agent, 16), r.Provider, in, out, r.Reason)
	}
	if p.Partial {
		fmt.Fprintln(stdout, "~ still counting since the dispatcher started")
	}
	return 0
}

// unattributedBudget bounds the host scan (the spec's 1 GiB): beyond, partial.
var unattributedBudget = tokens.Budget{Files: 1 << 20, Bytes: 1 << 30}

// tokensUnattributed scans the host's transcripts (and Codex rollouts) of the
// last 8 days and counts what no project's record names, per provider, today.
func tokensUnattributed(ctx context.Context, o tokensOpts, d tokensDeps, stdout, stderr io.Writer) int {
	names, err := d.Projects(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "agentbus tokens: "+err.Error())
		return 1
	}
	claimed := map[string]bool{}
	for _, n := range names {
		r, err := d.Open(n)
		if err != nil {
			continue
		}
		if recs, err := r.Sessions(ctx); err == nil {
			for id := range recs {
				claimed[id] = true
			}
		}
	}
	now := d.Now()
	cutoff := now.Add(-8 * 24 * time.Hour)
	today := now.In(d.Loc).Format("2006-01-02")
	recent := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			if info, err := os.Stat(p); err == nil && info.ModTime().After(cutoff) {
				out = append(out, p)
			}
		}
		return out
	}
	var claude []string
	tops, _ := filepath.Glob(filepath.Join(d.ClaudeRoot, "*", "*.jsonl"))
	subs, _ := filepath.Glob(filepath.Join(d.ClaudeRoot, "*", "*", "subagents", "*.jsonl"))
	seen := map[string]bool{} // a session counted once, whatever directory holds it
	for _, p := range recent(tops) {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if claimed[id] || seen[filepath.Base(p)] {
			continue
		}
		seen[filepath.Base(p)] = true
		claude = append(claude, p)
	}
	for _, p := range recent(subs) {
		if id := filepath.Base(filepath.Dir(filepath.Dir(p))); !claimed[id] {
			claude = append(claude, p)
		}
	}
	cs := tokens.NewClaudeScan(d.Loc)
	partial := cs.Pass(claude, unattributedBudget)
	var anth tokens.Usage
	for _, p := range claude {
		anth.Add(cs.Sums(p)[today])
	}
	var codexFiles []string
	for i := 0; i < 9; i++ { // rollouts live under their start date: the 8 days and today
		day := now.In(d.Loc).AddDate(0, 0, -i)
		m, _ := filepath.Glob(filepath.Join(d.CodexRoot, day.Format("2006"), day.Format("01"), day.Format("02"), "rollout-*.jsonl"))
		codexFiles = append(codexFiles, m...)
	}
	xs := tokens.NewCodexScan(d.Loc)
	if xs.Pass(codexFiles, unattributedBudget) {
		partial = true
	}
	var oai tokens.Usage
	for _, p := range codexFiles {
		oai.Add(xs.Sums(p)[today])
	}
	if o.json {
		return tokJSON(stdout, map[string]any{"day": today, "partial": partial,
			"providers": map[string]tokens.Usage{"anthropic": anth, "openai": oai},
			"note":      "sessions no project's record names; host scan, read-only"})
	}
	mark := ""
	if partial {
		mark = "~"
	}
	fmt.Fprintf(stdout, "not attributed today   anthropic %s%s in %s %s%s out   openai %s%s in %s %s%s out   (sessions no project's record names; host scan, read-only)\n",
		mark, tokCount(anth.In+anth.Write), "·", mark, tokCount(anth.Out), mark, tokCount(oai.In+oai.Write), "·", mark, tokCount(oai.Out))
	return 0
}

// tokSpark is one day's bar, scaled to max: a space for none.
func tokSpark(v, max int64) string {
	if v <= 0 || max <= 0 {
		return " "
	}
	blocks := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	i := int((v*int64(len(blocks)) - 1) / max)
	return blocks[min(tokMax0(i), len(blocks)-1)]
}

func tokMax0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

func tokMaxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// tokCount: 0, 873, 206k, 5.1M.
func tokCount(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}

func tokDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
}

func tokTrunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func tokJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}
