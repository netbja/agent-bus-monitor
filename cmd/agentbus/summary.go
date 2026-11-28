package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/attention"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

// agentbus summary (status-summary §3.1, §3.3, step 2): where the project
// stands, one line per section, read only. Every number comes from the
// broker; nothing is inferred. "Waiting on the person" is attention's, the
// definition busmon's For you uses (§3.2), minus reports and unread: those
// end on one busmon's local marks, so the summary counts the reports that ask
// for the person over 24 hours instead, never "unread".

// summaryVersion is the JSON's "v": additive changes only within it.
const summaryVersion = 1

// summaryWidth is the clip when stdout is not a terminal.
const summaryWidth = 100

// reportScan is how many report entries are read for the 24 h count.
const reportScan = 1000

// summaryOpts are the summary's settings: the person's identity ("" none),
// dispatch.stuck_after, and ci.loop (off: CI is not read).
type summaryOpts struct {
	Person     string
	StuckAfter time.Duration
	CILoop     bool
}

// summaryOptsFrom reads them as busmon does: identity.as, then person.name
// (busmon's default identity "pilot" is no person's).
func summaryOptsFrom(getenv func(string) string) summaryOpts {
	cfg := config.Load(config.Path(getenv), "config.toml")
	legacy := config.LoadLegacy(config.LegacyPath(getenv), "busmon.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{cfg, legacy}}
	person := ""
	if as := r.Get("identity.as"); !as.Invalid {
		person, _ = as.Value.(string)
	}
	if person == "" {
		person = r.String("person.name")
	}
	if !bus.ValidName(person) {
		person = ""
	}
	return summaryOpts{Person: person, StuckAfter: r.Duration("dispatch.stuck_after"), CILoop: r.Bool("ci.loop")}
}

// Summary is one project's summary, the JSON form (§3.3).
type Summary struct {
	V       int            `json:"v"`
	Project string         `json:"project"`
	ReadAt  time.Time      `json:"read_at"`
	ForYou  ForYouSection  `json:"for_you"`
	Merge   MergeSection   `json:"merge"`
	Blocked BlockedSection `json:"blocked"`
	Plan    PlanSection    `json:"plan"`
	Team    TeamSection    `json:"team"`
	Health  HealthSection  `json:"health"`
	Sources []string       `json:"sources"`
}

// Each section carries Unknown, the reason it could not be read ("" read).

type ForYouSection struct {
	Unknown string            `json:"unknown,omitempty"`
	Counts  map[string]int    `json:"counts"`                  // asks, waits, gates, merges, health, reports
	Named   map[string]string `json:"named,omitempty"`         // a kind's one item, named
	Merge   string            `json:"merge_unknown,omitempty"` // why the merge queue is unknown
	// ReportsAtLeast: the report scan reached its limit inside the 24 h, so
	// the reports count is a floor.
	ReportsAtLeast bool `json:"reports_at_least,omitempty"`
}

type MergeSection struct {
	Unknown string `json:"unknown,omitempty"`
	// NotComputed is why the dispatch service has not computed the queue
	// yet: no failed read, so no "?" and no exit 1.
	NotComputed string      `json:"not_computed,omitempty"`
	CIRead      bool        `json:"ci_read"`
	Items       []MergeLine `json:"items"`
}

type MergeLine struct {
	Number    int      `json:"number,omitempty"`
	Branch    string   `json:"branch"`
	Title     string   `json:"title,omitempty"`
	Approvers []string `json:"approvers"`
	CIRed     []string `json:"ci_red,omitempty"`
}

type BlockedSection struct {
	Unknown string        `json:"unknown,omitempty"`
	Count   int           `json:"count"`
	Tasks   []BlockedTask `json:"tasks"`
}

type BlockedTask struct {
	Task   string `json:"task"`
	Owner  string `json:"owner"`
	Reason string `json:"reason,omitempty"`
}

type PlanSection struct {
	Unknown    string `json:"unknown,omitempty"`
	Loaded     bool   `json:"loaded"`
	Plan       string `json:"plan,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	InProgress string `json:"in_progress,omitempty"`
	Owner      string `json:"owner,omitempty"`
	WaitsOnYou string `json:"waits_for_you,omitempty"`
}

type TeamSection struct {
	Unknown   string         `json:"unknown,omitempty"`
	Counts    map[string]int `json:"counts"`
	Anomalies []TeamAnomaly  `json:"anomalies,omitempty"`
}

type TeamAnomaly struct {
	Kind   string `json:"kind"`            // failed, stalled, quota-paused
	Check  string `json:"check,omitempty"` // the health check that says so (failed, stalled)
	Agent  string `json:"agent,omitempty"`
	Detail string `json:"detail"`
}

type HealthSection struct {
	Unknown string   `json:"unknown,omitempty"`
	Fail    []string `json:"fail"`
	Warn    []string `json:"warn"`
}

// failed is whether any section could not be read (exit 1).
func (s Summary) failed() bool {
	return s.ForYou.Unknown != "" || s.Merge.Unknown != "" || s.Blocked.Unknown != "" || s.Plan.Unknown != "" ||
		s.Team.Unknown != "" || s.Health.Unknown != ""
}

// waitingOnPerson: For you is not empty (--all's order).
func (s Summary) waitingOnPerson() bool {
	for _, n := range s.ForYou.Counts {
		if n > 0 {
			return true
		}
	}
	return s.ForYou.Merge != ""
}

// firstFailed is the first of names that failed, as "<name>: <err>".
func firstFailed(failed map[string]error, names ...string) string {
	for _, n := range names {
		if err := failed[n]; err != nil {
			return n + ": " + err.Error()
		}
	}
	return ""
}

// readSummary reads one project's summary at now.
// presenceReader is readPresences, injected: a test has no holder.
type presenceReader func(ctx context.Context, b *bus.Bus, project string, snaps map[string]bus.AgentSnapshot) map[string]agentPresence

func readSummary(ctx context.Context, b *bus.Bus, project string, o summaryOpts, presence presenceReader, now time.Time) Summary {
	s := Summary{V: summaryVersion, Project: project, ReadAt: now}
	in, failed := attention.Read(ctx, b, attention.ReadOpts{Person: o.Person, StuckAfter: o.StuckAfter})
	s.ForYou = forYouSection(ctx, b, in, failed, now)
	s.Merge = mergeSection(in, failed, o.CILoop)
	board, boardErr := b.Board(ctx)
	s.Blocked = blockedSection(board, boardErr)
	var commit string
	s.Plan, commit = planSection(ctx, b)
	snaps, snapsErr := b.Agents(ctx)
	marks, marksErr := b.QuotaMarks(ctx)
	var pres map[string]agentPresence
	if snapsErr == nil && presence != nil {
		pres = presence(ctx, b, project, snaps)
	}
	s.Team = teamSection(snaps, snapsErr, pres, in.Health, failed["health"], marks, marksErr, now)
	s.Health = healthSection(in.Health, failed["health"], now)
	planSrc := "plan"
	if s.Plan.Loaded {
		planSrc = "plan " + s.Plan.Plan + "@" + short7(commit)
	}
	s.Sources = []string{"asks", "merge-queue", "board", planSrc, "agents", "health"}
	return s
}

// forYouSection counts attention's items per kind, plus the reports of the
// last 24 hours that ask for the person (--needs read or answer).
func forYouSection(ctx context.Context, b *bus.Bus, in attention.Inputs, failed map[string]error, now time.Time) ForYouSection {
	f := ForYouSection{Counts: map[string]int{}, Named: map[string]string{}}
	// Every read Items judges with (reviewer-codex on step 2): the stall
	// guard needs the health records and the broker's clock, the merge
	// queue's unknown row the dispatch state.
	if why := firstFailed(failed, "asks", "agents", "gates", "observed", "cmd", "health", "clock", "dispatch"); why != "" {
		f.Unknown = why
		return f
	}
	kinds := map[int]string{attention.RankAsk: "asks", attention.RankHealth: "health", attention.RankWait: "waits",
		attention.RankGate: "gates", attention.RankMerge: "merges"}
	named := map[string][]string{}
	for _, it := range attention.Items(in, now) {
		k, ok := kinds[it.Rank]
		if !ok {
			continue
		}
		if it.Key == attention.MergeUnknownKey {
			f.Merge = it.Text
			continue
		}
		f.Counts[k]++
		named[k] = append(named[k], itemName(it, in))
	}
	evs, _, err := b.Recent(ctx, []string{"report"}, reportScan)
	if err != nil {
		f.Unknown = "reports: " + err.Error()
		return f
	}
	for _, e := range evs {
		if e.Needs != "" && now.Sub(bus.IDTime(e.ID)) <= 24*time.Hour {
			f.Counts["reports"]++
		}
	}
	// Every entry scanned is inside the 24 h and the scan reached its limit:
	// older ones were not read, so the count is a floor, said "≥" (foureyes).
	if len(evs) >= reportScan && allWithin(evs, now, 24*time.Hour) {
		f.ReportsAtLeast = true
	}
	for k, ns := range named {
		if len(ns) == 1 && ns[0] != "" {
			f.Named[k] = ns[0]
		}
	}
	return f
}

// itemName names a kind's one item: the PR, the gate's challenger and
// subject, the agent waiting or asking, the health check.
func itemName(it attention.Item, in attention.Inputs) string {
	switch it.Rank {
	case attention.RankMerge:
		for _, m := range in.Merge.Items {
			if attention.MergeKey(m) == it.Key {
				return mergeName(m)
			}
		}
	case attention.RankGate:
		return it.By + " on " + it.Text
	case attention.RankAsk, attention.RankWait:
		return it.Agent
	case attention.RankHealth:
		return it.Agent
	}
	return ""
}

func mergeName(m bus.MergeItem) string {
	if m.Number > 0 {
		return "#" + strconv.Itoa(m.Number)
	}
	return m.Branch
}

// mergeSection: each approved head waiting for the person, with its CI red
// checks, or that CI is not read while ci.loop is off. Never "green".
func mergeSection(in attention.Inputs, failed map[string]error, ciLoop bool) MergeSection {
	m := MergeSection{CIRead: ciLoop, Items: []MergeLine{}}
	if err := failed["merge-queue"]; err != nil {
		m.Unknown = "merge-queue: " + err.Error()
		return m
	}
	if in.Merge.ItemsAt == 0 {
		if err := failed["dispatch"]; err != nil { // why the queue is not computed is unknown
			m.Unknown = "dispatch: " + err.Error()
			return m
		}
		m.NotComputed = "not computed yet (the dispatch service computes it)"
		if why := attention.DispatchWhy(in.Dispatch); why != "" {
			m.NotComputed = "not computed: " + why
		}
		return m
	}
	for _, it := range in.Merge.Items {
		l := MergeLine{Number: it.Number, Branch: it.Branch, Title: it.Title, Approvers: it.Approvers}
		if l.Approvers == nil {
			l.Approvers = []string{}
		}
		if ciLoop {
			l.CIRed = it.CIRed
		}
		m.Items = append(m.Items, l)
	}
	return m
}

// blockedSection: the board's blocked tasks, oldest update first.
func blockedSection(board map[string]bus.BoardEntry, err error) BlockedSection {
	bs := BlockedSection{Tasks: []BlockedTask{}}
	if err != nil {
		bs.Unknown = "board: " + err.Error()
		return bs
	}
	var names []string
	for n, e := range board {
		if e.State == "blocked" {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(a, c int) bool {
		if board[names[a]].Updated != board[names[c]].Updated {
			return board[names[a]].Updated < board[names[c]].Updated
		}
		return names[a] < names[c]
	})
	for _, n := range names {
		e := board[n]
		t := BlockedTask{Task: n, Owner: e.Owner}
		if e.Request != nil {
			t.Reason = e.Request.BlockedReason
		}
		bs.Tasks = append(bs.Tasks, t)
	}
	bs.Count = len(bs.Tasks)
	return bs
}

// planSection: done/total, the task in progress (plan.NowNext) and the first
// task waiting for the person; the commit for the sources line.
func planSection(ctx context.Context, b *bus.Bus) (PlanSection, string) {
	act, ok, err := plan.Load(ctx, b)
	if err != nil {
		return PlanSection{Unknown: "plan: " + err.Error()}, ""
	}
	if !ok {
		return PlanSection{}, ""
	}
	in, errs := plan.Read(ctx, b, act.File)
	p := PlanSection{Loaded: true, Plan: act.File.Plan, Commit: act.Record.Commit}
	if len(errs) > 0 {
		p.Unknown = "plan: " + errs[0].Error()
		return p, act.Record.Commit
	}
	rows := plan.Derive(act.File, in)
	p.Done, p.Total = plan.Count(rows)
	if now, _ := plan.NowNext(rows); now != nil && (now.State == plan.InProgress || now.State == plan.InReview) {
		p.InProgress, p.Owner = now.Task.ID, now.Task.Owner
	}
	for _, r := range rows {
		if r.State == plan.Waiting {
			p.WaitsOnYou = r.Task.ID
			if r.PR > 0 {
				p.WaitsOnYou = "#" + strconv.Itoa(r.PR)
			}
			break
		}
	}
	return p, act.Record.Commit
}

// teamStates are the counted states, in the line's order.
var teamStates = []string{"working", "waiting", "blocked", "idle", "done"}

// teamSection: counts by state; the anomalies named from their health
// records (agent-launch, self-repair: failed; idle-implementer: stalled),
// never recomputed, and the agents whose provider is on a quota hold.
func teamSection(snaps map[string]bus.AgentSnapshot, err error, pres map[string]agentPresence, records map[string]health.Record, healthErr error,
	marks map[string]string, marksErr error, now time.Time) TeamSection {
	t := TeamSection{Counts: map[string]int{}}
	if err != nil {
		t.Unknown = "agents: " + err.Error()
		return t
	}
	// The anomalies are read, never recomputed: health records or quota marks
	// that cannot be read leave them unknown, so the line is "?" (foureyes).
	switch {
	case healthErr != nil:
		t.Unknown = "health: " + healthErr.Error()
		return t
	case marksErr != nil:
		t.Unknown = "quota: " + marksErr.Error()
		return t
	}
	names := make([]string, 0, len(snaps))
	for n, s := range snaps {
		names = append(names, n)
		// A lost agent (cleaning-up-agents §3: no terminal, and it owes
		// something or is required) is failed, never its last state
		// (reviewer-codex on step 2).
		if p := pres[n]; p.State == bus.PresenceLost {
			t.Counts["failed"]++
			continue
		}
		if s.TS != 0 && slices.Contains(teamStates, s.State) {
			t.Counts[s.State]++
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if p := pres[n]; p.State == bus.PresenceLost {
			t.Anomalies = append(t.Anomalies, TeamAnomaly{Kind: "failed", Agent: n, Detail: "lost: " + p.Reason})
		}
	}
	for _, c := range []struct{ id, kind string }{{"agent-launch", "failed"}, {"self-repair", "failed"}, {"idle-implementer", "stalled"}} {
		rec, ok := records[c.id]
		d, known := health.ByID(c.id)
		if !ok || !known {
			continue
		}
		if st := health.EffectiveStatus(rec, now.UnixMilli(), d.Interval); st == health.Warn || st == health.Fail {
			t.Anomalies = append(t.Anomalies, TeamAnomaly{Kind: c.kind, Check: c.id, Detail: rec.Detail})
			if c.kind == "failed" {
				t.Counts["failed"]++
			}
		}
	}
	for _, n := range names {
		s := snaps[n]
		if bus.HeldReason(marks, n, s.Provider) == bus.HeldQuota {
			t.Anomalies = append(t.Anomalies, TeamAnomaly{Kind: "quota-paused", Agent: n, Detail: s.Provider + " at its limit"})
		}
	}
	return t
}

// healthSection: the checks in fail, then in warn, by name.
func healthSection(records map[string]health.Record, err error, now time.Time) HealthSection {
	h := HealthSection{Fail: []string{}, Warn: []string{}}
	if err != nil {
		h.Unknown = "health: " + err.Error()
		return h
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var interval time.Duration
		if d, ok := health.ByID(id); ok {
			interval = d.Interval
		}
		switch health.EffectiveStatus(records[id], now.UnixMilli(), interval) {
		case health.Fail:
			h.Fail = append(h.Fail, id)
		case health.Warn:
			h.Warn = append(h.Warn, id)
		}
	}
	return h
}

// lines is the text form, one line per section and the sources line.
func (s Summary) lines() []string {
	row := func(label, text string) string { return fmt.Sprintf("%-9s %s", label, text) }
	unknown := func(why string) string { return "? " + why }
	out := []string{s.Project + " · " + s.ReadAt.Local().Format("15:04")}

	f := s.ForYou
	switch {
	case f.Unknown != "":
		out = append(out, row("For you", unknown(f.Unknown)))
	default:
		var parts []string
		for _, k := range []struct{ key, one, many string }{
			{"asks", "ask", "asks"}, {"waits", "agent waiting", "agents waiting"}, {"health", "stall", "stalls"},
			{"merges", "merge", "merges"}, {"gates", "gate", "gates"}, {"reports", "report (24 h)", "reports (24 h)"}} {
			n := f.Counts[k.key]
			if n == 0 {
				continue
			}
			p := fmt.Sprintf("%d %s", n, k.many)
			if k.key == "reports" && f.ReportsAtLeast {
				p = "≥" + p
			} else if n == 1 {
				p = "1 " + k.one
				if name := f.Named[k.key]; name != "" && k.key != "reports" {
					p += " (" + name + ")"
				}
			}
			parts = append(parts, p)
		}
		if f.Merge != "" {
			parts = append(parts, f.Merge)
		}
		out = append(out, row("For you", orNone(parts)))
	}

	m := s.Merge
	if m.Unknown != "" {
		out = append(out, row("Merge", unknown(m.Unknown)))
	} else if m.NotComputed != "" {
		out = append(out, row("Merge", m.NotComputed))
	} else {
		var parts []string
		for _, it := range m.Items {
			p := mergeName(bus.MergeItem{Number: it.Number, Branch: it.Branch})
			if it.Title != "" {
				p += " " + it.Title
			}
			p += fmt.Sprintf(" · approved by %d", len(it.Approvers))
			switch {
			case !m.CIRead:
				p += " · CI not read (ci.loop off)"
			case len(it.CIRed) > 0:
				p += " · CI red: " + strings.Join(it.CIRed, ", ")
			}
			parts = append(parts, p)
		}
		out = append(out, row("Merge", orNoneSep(parts, "; ")))
	}

	bl := s.Blocked
	if bl.Unknown != "" {
		out = append(out, row("Blocked", unknown(bl.Unknown)))
	} else if bl.Count == 0 {
		out = append(out, row("Blocked", "none"))
	} else {
		parts := []string{strconv.Itoa(bl.Count)}
		for i, t := range bl.Tasks {
			if i == 2 {
				parts = append(parts, fmt.Sprintf("+%d more", bl.Count-2))
				break
			}
			p := t.Task + " (" + t.Owner + ")"
			if t.Reason != "" {
				p += ": " + t.Reason
			}
			parts = append(parts, p)
		}
		out = append(out, row("Blocked", strings.Join(parts, " · ")))
	}

	p := s.Plan
	switch {
	case p.Unknown != "":
		out = append(out, row("Plan", unknown(p.Unknown)))
	case !p.Loaded:
		out = append(out, row("Plan", "no plan loaded"))
	default:
		parts := []string{fmt.Sprintf("%s %d/%d", p.Plan, p.Done, p.Total)}
		if p.InProgress != "" {
			parts = append(parts, "in progress: "+p.InProgress+" ("+p.Owner+")")
		}
		if p.WaitsOnYou != "" {
			parts = append(parts, "waits for you: "+p.WaitsOnYou)
		}
		out = append(out, row("Plan", strings.Join(parts, " · ")))
	}

	t := s.Team
	if t.Unknown != "" {
		out = append(out, row("Team", unknown(t.Unknown)))
	} else {
		var parts []string
		for _, st := range append(slices.Clone(teamStates), "failed") {
			if n := t.Counts[st]; n > 0 || st == "failed" {
				parts = append(parts, fmt.Sprintf("%d %s", n, st))
			}
		}
		for _, a := range t.Anomalies {
			w := a.Kind
			if a.Check != "" {
				w += " (" + a.Check + ")"
			}
			if a.Agent != "" {
				w += " " + a.Agent
			}
			parts = append(parts, w+": "+a.Detail)
		}
		out = append(out, row("Team", strings.Join(parts, " · ")))
	}

	h := s.Health
	switch {
	case h.Unknown != "":
		out = append(out, row("Health", unknown(h.Unknown)))
	case len(h.Fail)+len(h.Warn) == 0:
		out = append(out, row("Health", "none"))
	default:
		var parts []string
		if len(h.Fail) > 0 {
			parts = append(parts, fmt.Sprintf("%d fail: %s", len(h.Fail), strings.Join(h.Fail, ", ")))
		}
		if len(h.Warn) > 0 {
			parts = append(parts, fmt.Sprintf("%d warn: %s", len(h.Warn), strings.Join(h.Warn, ", ")))
		}
		out = append(out, row("Health", strings.Join(parts, " · ")))
	}
	out = append(out, "↳ "+strings.Join(s.Sources, ", ")+" · read "+s.ReadAt.Local().Format("15:04:05"))
	return out
}

func orNone(parts []string) string { return orNoneSep(parts, " · ") }

func orNoneSep(parts []string, sep string) string {
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, sep)
}

// clipLines clips each line to width columns with "…".
func clipLines(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, width, "…")
	}
	return out
}

// summaryArgs: --all, --json; nothing else.
type summaryArgs struct{ all, json bool }

var errSummaryUsage = fmt.Errorf("usage: agentbus summary [--project <p>] [--all] [--json]")

func parseSummaryArgs(args []string) (summaryArgs, error) {
	var a summaryArgs
	for _, x := range args {
		switch x {
		case "--all":
			a.all = true
		case "--json":
			a.json = true
		default:
			return a, errSummaryUsage
		}
	}
	return a, nil
}

// summaryDeps: the projects to read (--all: bus.Projects' list), a bus for
// each, the options, the clock and the width.
type summaryDeps struct {
	Projects func(ctx context.Context) ([]string, error)
	Open     func(project string) (*bus.Bus, error)
	Opts     summaryOpts
	Presence presenceReader // nil: no presence read, every agent its declared state
	Now      func() time.Time
	Width    int
}

// runSummary prints the summary of project, or of every project with --all
// (those with something waiting on the person first, then in the list's
// order). Exit 0 when every read succeeded, 1 when one failed, 2 on usage.
func runSummary(ctx context.Context, project string, args []string, d summaryDeps, out, errw io.Writer) int {
	a, err := parseSummaryArgs(args)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	projects := []string{project}
	if a.all {
		if projects, err = d.Projects(ctx); err != nil {
			fmt.Fprintln(errw, "agentbus summary: the projects cannot be listed: "+err.Error())
			return 1
		}
	} else if !bus.ValidName(project) {
		fmt.Fprintln(errw, "agentbus summary: project required: pass --project <p>, or --all")
		return 2
	}
	now := d.Now()
	var sums []Summary
	code := 0
	for _, p := range projects {
		b, err := d.Open(p)
		if err != nil {
			fmt.Fprintf(errw, "agentbus summary: %s: %v\n", p, err)
			code = 1
			continue
		}
		s := readSummary(ctx, b, p, d.Opts, d.Presence, now)
		if s.failed() {
			code = 1
		}
		sums = append(sums, s)
	}
	if a.all {
		sort.SliceStable(sums, func(i, j int) bool { return sums[i].waitingOnPerson() && !sums[j].waitingOnPerson() })
	}
	if a.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if a.all {
			enc.Encode(struct {
				V        int       `json:"v"`
				Projects []Summary `json:"projects"`
			}{summaryVersion, sums})
		} else if len(sums) == 1 {
			enc.Encode(sums[0])
		}
		return code
	}
	width := d.Width
	if width <= 0 {
		width = summaryWidth
	}
	for i, s := range sums {
		if i > 0 {
			fmt.Fprintln(out)
		}
		for _, l := range clipLines(s.lines(), width) {
			fmt.Fprintln(out, l)
		}
	}
	return code
}

// mainSummary is the command's entry, above the project-required exit
// (--all needs none): one connection, a bus per project, the width of the
// terminal when stdout is one.
func mainSummary(host, project string, args []string) int {
	client, err := bus.Connect(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentbus summary: Redis connection failed: %v\n", err)
		return 1
	}
	defer client.Close()
	width := 0
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) {
		width, _, _ = term.GetSize(fd)
	}
	d := summaryDeps{
		Projects: func(ctx context.Context) ([]string, error) {
			ps, err := bus.Projects(ctx, client)
			var names []string
			for _, p := range ps {
				names = append(names, p.Project)
			}
			return names, err
		},
		Open:     func(p string) (*bus.Bus, error) { return bus.Open(client, p) },
		Opts:     summaryOptsFrom(os.Getenv),
		Presence: readPresences,
		Now:      time.Now,
		Width:    width,
	}
	return runSummary(context.Background(), project, args, d, os.Stdout, os.Stderr)
}

// statusUsage is the usage error of status with fewer than two positionals
// (status-summary §3.1): exit 2, pointing to summary.
const statusUsage = "usage: status <agent> <state> [message...] (the project's summary is agentbus summary)"

// allWithin: every event of evs is no older than d at now.
func allWithin(evs []bus.Event, now time.Time, d time.Duration) bool {
	for _, e := range evs {
		if now.Sub(bus.IDTime(e.ID)) > d {
			return false
		}
	}
	return true
}
