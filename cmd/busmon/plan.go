package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/plan"
)

// planReader is what the plan view reads (spec
// docs/superpowers/specs/2026-10-22-plan-view-design.md). *bus.Bus satisfies
// it; a reader without it (the older fakes) shows no plan.
type planReader interface {
	Plan(ctx context.Context) (bus.PlanRecord, bool, error)
	PlanFacts(ctx context.Context) (map[string]bus.PlanFact, error)
	VerdictsAfter(ctx context.Context, after string, count int64) ([]bus.Verdict, error)
	ReadMissionHash(ctx context.Context, key string) (map[string]string, error)
}

// Ledger pages: one tick reads at most planLedgerPages × planLedgerPage new
// verdicts, from the last seen id; the first read catches up over a few ticks.
const (
	planLedgerPage  = 500
	planLedgerPages = 4
)

// planState is the plan view's model. Its kept values survive a failed read;
// rows are derived in pollOnce, never in a draw.
type planState struct {
	loaded  bool // a plan is loaded and its last good read parsed
	rec     bus.PlanRecord
	file    plan.File
	cursor  string                   // the newest verdict id read
	ledger  map[string][]bus.Verdict // every subject's verdicts read so far (slim)
	in      plan.Inputs              // kept values and their ever-read flags
	failed  map[string]bool          // inputs whose latest read failed
	rows    []plan.Row
	partial bool // some row derives from a kept value of a failed read
}

// planReads are this tick's plan reads, done before the shared lock.
type planReads struct {
	polled   bool
	rec      bus.PlanRecord
	has      bool
	recErr   error
	facts    map[string]bus.PlanFact
	factsErr error
	verdicts []bus.Verdict
	caughtUp bool // the last page was short: the cursor reached the ledger's end
	vErr     error
	missions map[string]mission.State
	mErr     error
}

// readPlan does the plan reads of a requestPoll tick: one GET when no plan
// is loaded; with one, its facts, the new verdicts and its missions.
func readPlan(ctx context.Context, b busReader, cursor string, f plan.File, known bool) planReads {
	pr, ok := b.(planReader)
	if !ok {
		return planReads{}
	}
	r := planReads{polled: true}
	r.rec, r.has, r.recErr = pr.Plan(ctx)
	if r.recErr != nil || !r.has {
		return r
	}
	r.facts, r.factsErr = pr.PlanFacts(ctx)
	for range planLedgerPages {
		page, err := pr.VerdictsAfter(ctx, cursor, planLedgerPage)
		if err != nil {
			r.vErr = err
			break
		}
		for _, v := range page {
			v.Message, v.Full = "", "" // only the roll-up fields are kept
			r.verdicts = append(r.verdicts, v)
		}
		if len(page) < planLedgerPage {
			r.caughtUp = true
			break
		}
		cursor = page[len(page)-1].ID
	}
	if pf, err := plan.Parse([]byte(r.rec.File)); err == nil {
		f, known = pf, true
	}
	if known {
		r.missions = map[string]mission.State{}
		for _, c := range plan.Changes(f) {
			h, err := pr.ReadMissionHash(ctx, bus.MissionsKey(projectOf(b), c))
			if err != nil {
				r.mErr = err
				break
			}
			for id, raw := range h {
				var m struct {
					State mission.State `json:"state"`
				}
				if json.Unmarshal([]byte(raw), &m) == nil {
					r.missions[c+"/"+id] = m.State
				}
			}
		}
	}
	return r
}

// projectOf names the project for a missions key: the reader's own project.
func projectOf(b busReader) string {
	if p, ok := b.(interface{ Project() string }); ok {
		return p.Project()
	}
	return ""
}

// fold merges this tick's reads into the kept values and derives the rows.
// It runs under st.mu; board and asks are the tick's own reads.
func (p *planState) fold(r planReads, board map[string]bus.BoardEntry, boardErr error, asks []bus.Ask, asksErr error) {
	if p.failed == nil {
		p.failed = map[string]bool{}
		p.ledger = map[string][]bus.Verdict{}
	}
	if r.polled {
		switch {
		case r.recErr != nil:
			p.failed["plan"] = true
		case !r.has:
			*p = planState{failed: map[string]bool{}, ledger: p.ledger, cursor: p.cursor}
			return
		default:
			p.failed["plan"] = false
			if f, err := plan.Parse([]byte(r.rec.File)); err == nil {
				p.rec, p.file, p.loaded = r.rec, f, true
			} else {
				p.failed["plan"] = true
			}
			setRead(p, "facts", r.factsErr, func() { p.in.Facts, p.in.FactsOK = r.facts, true })
			setRead(p, "verdicts", r.vErr, func() {})
			for _, v := range r.verdicts {
				p.ledger[v.Subject] = append(p.ledger[v.Subject], v)
				p.cursor = v.ID
			}
			if r.caughtUp { // a review read mid catch-up could miss its verdict: '?' until then
				p.in.VerdictsOK = true
			}
			setRead(p, "missions", r.mErr, func() { p.in.Missions, p.in.MissionsOK = r.missions, r.missions != nil })
		}
	}
	if !p.loaded {
		return
	}
	setRead(p, "board", boardErr, func() { p.in.Board, p.in.BoardOK = board, true })
	setRead(p, "asks", asksErr, func() { p.in.Asks, p.in.AsksOK = asks, true })
	p.in.Verdicts = p.ledger
	p.rows = plan.Derive(p.file, p.in)
	p.partial = p.failed["plan"] || p.failed["facts"] || p.failed["verdicts"] || p.failed["board"] || p.failed["asks"] || p.failed["missions"]
}

// kept reports a row derived from a value kept after a failed read: the
// inputs it depends on (missions for a mission task, else board, facts,
// verdicts and asks) or the plan itself. A never-read input is plan.Unknown.
func (p planState) kept(r plan.Row) bool {
	if r.State == plan.Unknown || p.failed["plan"] {
		return r.State != plan.Unknown && p.failed["plan"]
	}
	if r.Task.Mission != "" {
		return p.failed["missions"]
	}
	return p.failed["board"] || p.failed["facts"] || p.failed["verdicts"] || p.failed["asks"]
}

func setRead(p *planState, name string, err error, keep func()) {
	p.failed[name] = err != nil
	if err == nil {
		keep()
	}
}

// planSummary is the header's plan item.
type planSummary struct {
	name           string
	done, total    int
	person         int // tasks waiting for the person
	partial, stale bool
}

func (p planState) summary(now time.Time) (planSummary, bool) {
	if !p.loaded {
		return planSummary{}, false
	}
	s := planSummary{name: p.file.Plan, partial: p.partial, stale: now.Sub(time.UnixMilli(p.rec.LoadedAt)) > plan.StaleAfter}
	s.done, s.total = plan.Count(p.rows)
	for _, r := range p.rows {
		if r.State == plan.Waiting {
			s.person++
		}
	}
	return s, true
}

// planSeg renders the header item: "plan v0.22 3/9", a ? when it derives
// from a kept value, ♙ N when tasks wait for the person.
func planSeg(s planSummary, now time.Time) Line {
	count := fmt.Sprintf("%d/%d", s.done, s.total)
	if s.partial {
		count += "?"
	}
	l := Line{{Text: "plan " + Preview(s.name) + " " + count, Role: RoleMuted}}
	if s.stale {
		l = append(l, Seg{Text: " stale", Role: RoleMuted})
	}
	if s.person > 0 {
		l = append(l, Seg{Text: fmt.Sprintf(" %s %d", GlyphHuman, s.person), Role: RoleAttention})
	}
	return l
}

// stateLook is a state's glyph and role (theme.go only; a glyph per state, so
// the 16-colour and no-colour palettes stay readable).
func planLook(s plan.State) (string, Role) {
	switch s {
	case plan.Waiting:
		return GlyphHuman, RoleAttention
	case plan.Answered: // master's to record, no longer the person's
		return GlyphDotEmpty, RoleAccent
	case plan.Blocked:
		return GlyphBlocked, RoleError
	case plan.InReview:
		return GlyphDotEmpty, RoleDispatch
	case plan.InProgress:
		return GlyphWorking, RoleAccent
	case plan.Done:
		return GlyphDone, RoleDone
	case plan.Unknown:
		return "?", RoleMuted
	}
	return GlyphIdle, RoleMuted
}

// planItem is a selectable line of the plan board: a task, by its line.
type planItem struct {
	task string
	line int // its line in the body, for scrolling
}

func planMark(selected bool) Line {
	if selected {
		return Line{S(RoleAccent, GlyphSelect), S(RoleText, " ")}
	}
	return Line{S(RoleText, "  ")}
}

// planShortRef is a delivered ref, <branch>@<sha>, its sha cut to 7.
func planShortRef(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 && len(ref)-i-1 > 7 { // the sha follows the last "@"
		branch, sha := ref[:i], ref[i+1:]
		return Preview(branch) + "@" + sha[:7]
	}
	return Preview(ref)
}

// planRowDetails: PR, verdict, the done label, why blocked, what it waits on,
// the ask, quiet, the note.
func planRowDetails(r plan.Row, now time.Time) string {
	var parts []string
	if r.PR > 0 {
		parts = append(parts, fmt.Sprintf("#%d", r.PR))
	}
	if r.Ref != "" {
		parts = append(parts, "ref "+planShortRef(r.Ref))
	}
	if r.Verdict != "" && r.State != plan.Done {
		parts = append(parts, r.Verdict)
	}
	switch {
	case r.State == plan.Done:
		parts = append(parts, plan.DoneLabel(r, now))
	case r.Reason != "":
		parts = append(parts, Preview(r.Reason))
	}
	if len(r.WaitingOn) > 0 && r.State == plan.Upcoming {
		parts = append(parts, "waiting on "+strings.Join(r.WaitingOn, ", "))
	}
	if r.Ask != "" {
		parts = append(parts, "a question for you (! answers)")
	}
	if plan.Quiet(r, now) {
		parts = append(parts, "quiet "+plan.Age(r.LastFact, now))
	}
	if r.LastFact > 0 && r.State != plan.Done {
		parts = append(parts, plan.Age(r.LastFact, now))
	}
	if r.Note != "" {
		parts = append(parts, Preview(r.Note))
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

// planTaskDetail is the detail of one task: its row, its board entry and
// request, its verdicts per subject, its ask and the recorded facts.
func planTaskDetail(p planState, id string, now time.Time) (title string, body []Line, plain string) {
	var r plan.Row
	for _, x := range p.rows {
		if x.Task.ID == id {
			r = x
		}
	}
	var f fields
	add := func(k, v string) {
		if v != "" {
			f.add(k, S(RoleText, v))
		}
	}
	state := plan.Label(r.State)
	if r.State == plan.Done {
		state = plan.DoneLabel(r, now)
	}
	add("state", state)
	add("title", Preview(r.Task.Title))
	add("owner", r.Task.Owner)
	if _, ok := planStartable(p, id); ok { // the person's own task: the way to act on it, a row of the block
		key, _, _ := strings.Cut(tourKeys(tourRef{m: ModeOverlay, act: ActPlanStart}), " ")
		f.add("action", S(RoleAccent, key), S(RoleText, "  start the discussion with master"))
	}
	if e, ok := p.in.Board[r.Task.Board]; ok && r.Task.Board != "" {
		v := r.Task.Board + ": " + e.State
		if e.Branch != "" {
			v += " (" + e.Branch + ")"
		}
		if e.Request != nil {
			v += " " + GlyphNone + " request " + e.Request.ID
		}
		add("board", Preview(v))
	} else if r.Task.Board != "" {
		add("board", r.Task.Board+": not on the board")
	}
	if r.Task.Mission != "" {
		add("mission", r.Task.Mission)
	}
	if r.PR > 0 {
		add("PR", fmt.Sprintf("#%d", r.PR))
	}
	if r.Ref != "" {
		add("ref", planShortRef(r.Ref))
	}
	for _, s := range plan.Subjects(r.Task, p.in.Facts[id]) {
		var vs []string
		for _, v := range p.ledger[s] {
			vs = append(vs, v.Reviewer+" "+v.Decision)
		}
		if len(vs) == 0 {
			vs = []string{plan.NoVerdict}
		}
		add("review", Preview(s+": "+strings.Join(vs, ", ")))
	}
	if r.Ask != "" {
		add("ask", r.Ask+" (! answers it)")
	}
	if len(r.WaitingOn) > 0 {
		add("after", strings.Join(r.WaitingOn, ", "))
	}
	add("note", Preview(r.Note))
	if r.LastFact > 0 {
		add("last fact", plan.Age(r.LastFact, now)+" ago")
	}
	var lines []string
	for _, l := range f.lines(1 << 20) { // the copy: one line per row, unwrapped
		lines = append(lines, l.text())
	}
	plain = strings.Join(lines, "\n")
	body = f.lines(mdWidth)
	if len(body) == 0 {
		body = []Line{nil}
	}
	return "Plan " + GlyphNone + " " + id, body, plain
}

// planStartable is the task when it waits for the person themselves: theirs,
// its dependencies done (the plan's own Waiting), not an agent's open ask.
func planStartable(p planState, id string) (plan.Row, bool) {
	for _, r := range p.rows {
		if r.Task.ID == id {
			return r, r.State == plan.Waiting && r.Task.Owner == plan.Person
		}
	}
	return plan.Row{}, false
}

// planStartText is what the person writes to master to start the task.
func planStartText(r plan.Row) string {
	return "Plan " + Preview(r.Task.ID) + ": I start this one - " + Preview(r.Task.Title)
}

func short7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// planRecapExpected adds the plan's tasks waiting for the person to
// recapForYou; one waiting on an open ask the recap already lists
// is not listed twice.
func planRecapExpected(in recapInput, add func(recapRow)) {
	if in.Plan == nil || !in.Plan.loaded {
		return
	}
	listed := map[string]bool{}
	for _, a := range openAsks(in.Asks) {
		listed[a.ID] = true
	}
	for _, r := range in.Plan.rows {
		if r.State != plan.Waiting || (r.Ask != "" && listed[r.Ask]) {
			continue
		}
		add(recapRow{Key: "plan:" + r.Task.ID, Section: recapForYou, Title: "plan " + r.Task.ID + " " + GlyphNone + " " + plan.Label(r.State),
			Detail: Preview(r.Task.Title) + " " + GlyphNone + " " + planRowDetails(r, in.Now)})
	}
}

// planRecapSection is the recap's "Plan" section: the plan line, then the
// tasks whose newest fact is after the last visit, with their state now. A
// change is listed only when a retained fact proves it; the earlier state is
// not stored, so it is never guessed.
func planRecapSection(in recapInput, add func(recapRow), section func(string)) {
	if in.Plan == nil || !in.Plan.loaded {
		return
	}
	p := *in.Plan
	section("Plan")
	sum, _ := p.summary(in.Now)
	add(recapRow{Section: "Plan", Title: planSeg(sum, in.Now).text(), Detail: Preview(p.file.Goal)})
	if in.Since.IsZero() {
		add(recapRow{Section: "Plan", Title: "no previous visit recorded; changes cannot be shown"})
		return
	}
	n := 0
	for _, r := range p.rows {
		if r.LastFact <= in.Since.UnixMilli() {
			continue
		}
		state := plan.Label(r.State)
		if r.State == plan.Done {
			state = plan.DoneLabel(r, in.Now)
		}
		add(recapRow{Key: "planchg:" + r.Task.ID, Section: "Plan",
			Title:  r.Task.ID + " " + GlyphArrowR + " " + state + " " + GlyphNone + " " + plan.Age(r.LastFact, in.Now) + " ago",
			Detail: Preview(r.Task.Title)})
		n++
	}
	if n == 0 {
		add(recapRow{Section: "Plan", Title: "no task changed since your last visit"})
	}
}
