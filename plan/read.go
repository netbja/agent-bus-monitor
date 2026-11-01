package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
)

// Active is the loaded plan: its record and its parsed file.
type Active struct {
	Record bus.PlanRecord
	File   File
}

// Load reads the active plan; ok is false when none is loaded.
func Load(ctx context.Context, b *bus.Bus) (Active, bool, error) {
	rec, ok, err := b.Plan(ctx)
	if err != nil || !ok {
		return Active{}, ok, err
	}
	f, err := Parse([]byte(rec.File))
	if err != nil {
		return Active{}, false, err
	}
	return Active{Record: rec, File: f}, true, nil
}

// Read reads every input of Derive once, for agentbus plan show: the whole
// verdict ledger in one read, the missions of the plan's changes. A failed
// read leaves its OK flag false and is returned.
func Read(ctx context.Context, b *bus.Bus, f File) (Inputs, []error) {
	all, err := b.Verdicts(ctx, "")
	if err != nil {
		in, errs := ReadWithVerdicts(ctx, b, f, nil, false)
		return in, append(errs, fmt.Errorf("verdicts: %w", err))
	}
	return ReadWithVerdicts(ctx, b, f, BySubject(all, nil), true)
}

// ReadWithVerdicts is Read with the verdicts given by the caller (a poller
// that reads the ledger forward keeps its own), ok false when it has none.
func ReadWithVerdicts(ctx context.Context, b *bus.Bus, f File, verdicts map[string][]bus.Verdict, ok bool) (Inputs, []error) {
	var errs []error
	in := Inputs{Missions: map[string]mission.State{}, Verdicts: verdicts, VerdictsOK: ok}
	var err error
	if in.Facts, err = b.PlanFacts(ctx); err == nil {
		in.FactsOK = true
	} else {
		errs = append(errs, fmt.Errorf("facts: %w", err))
	}
	if in.Board, err = b.Board(ctx); err == nil {
		in.BoardOK = true
	} else {
		errs = append(errs, fmt.Errorf("board: %w", err))
	}
	if in.Asks, err = b.Asks(ctx); err == nil {
		in.AsksOK = true
	} else {
		errs = append(errs, fmt.Errorf("asks: %w", err))
	}
	in.MissionsOK = true
	for _, c := range Changes(f) {
		recs, err := (mission.Store{B: b}).Missions(ctx, c)
		if err != nil {
			in.MissionsOK = false
			errs = append(errs, fmt.Errorf("missions %s: %w", c, err))
			continue
		}
		for id, r := range recs {
			in.Missions[c+"/"+id] = r.State
		}
	}
	return in, errs
}

// BySubject buckets verdicts by subject, in ledger order, keeping only the
// subjects in keep (all when keep is nil).
func BySubject(vs []bus.Verdict, keep map[string]bool) map[string][]bus.Verdict {
	out := map[string][]bus.Verdict{}
	for _, v := range vs {
		if keep == nil || keep[v.Subject] {
			out[v.Subject] = append(out[v.Subject], v)
		}
	}
	return out
}

// Changes are the distinct mission changes the plan names.
func Changes(f File) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range f.Tasks {
		if c, _, ok := strings.Cut(t.Mission, "/"); ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// AllSubjects are the verdict subjects the plan's reviews read.
func AllSubjects(f File, facts map[string]Fact) map[string]bool {
	out := map[string]bool{}
	for _, t := range f.Tasks {
		for _, s := range Subjects(t, facts[t.ID]) {
			out[s] = true
		}
	}
	return out
}

// Label is how a state reads.
func Label(s State) string {
	switch s {
	case Waiting:
		return "waiting for you"
	case InReview:
		return "in review"
	case InProgress:
		return "in progress"
	}
	return string(s)
}

// DoneLabel says how a done task ended: a recorded claim carries its age.
func DoneLabel(r Row, now time.Time) string {
	switch {
	case r.Recorded && r.Task.MergeTask():
		return "merged (recorded " + Age(r.DoneAt, now) + " ago)"
	case r.Recorded:
		return "done (recorded " + Age(r.DoneAt, now) + " ago)"
	case r.Reason != "":
		return r.Reason
	}
	return "done"
}

// Age renders a Unix-ms time as a short age: 45s, 12m, 3h, 2d.
func Age(ms int64, now time.Time) string {
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// StaleAfter is when a plan's load reads stale; QuietAfter is when a task
// in progress with no newer fact reads quiet.
const (
	StaleAfter = 7 * 24 * time.Hour
	QuietAfter = 24 * time.Hour
)

// Quiet reports a task in progress whose newest fact is older than QuietAfter.
func Quiet(r Row, now time.Time) bool {
	return r.State == InProgress && r.LastFact > 0 && now.Sub(time.UnixMilli(r.LastFact)) > QuietAfter
}

// Count is done/total.
func Count(rows []Row) (done, total int) {
	for _, r := range rows {
		if r.State == Done {
			done++
		}
	}
	return done, len(rows)
}
