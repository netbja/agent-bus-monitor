package plan

import (
	"sort"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
)

// State is where a plan task stands, derived, never typed.
type State string

const (
	Done       State = "done"
	Waiting    State = "waiting" // waiting for the person
	Blocked    State = "blocked"
	InReview   State = "review"
	InProgress State = "progress"
	Upcoming   State = "upcoming"
	Unknown    State = "?" // a read the deciding rule depends on never succeeded
)

// Order is the display order of the states (spec: waiting for you first,
// done last).
var Order = []State{Waiting, Blocked, InReview, InProgress, Upcoming, Unknown, Done}

// Verdict labels of a task's review.
const (
	NoVerdict    = "no verdict yet"
	Pending      = "pending"
	Approved     = "approved"
	ChangesAsked = "changes asked"
)

// Fact is what master recorded about a task in {p}:plan:facts.
type Fact = bus.PlanFact

// Inputs are the facts Derive reads, as already read. Each *OK says whether
// that read has ever succeeded; a kept older value is still OK.
type Inputs struct {
	Board    map[string]bus.BoardEntry
	Facts    map[string]Fact
	Verdicts map[string][]bus.Verdict // by subject, ledger order
	Asks     []bus.Ask
	Missions map[string]mission.State // by "<change>/<id>"

	BoardOK, FactsOK, VerdictsOK, AsksOK, MissionsOK bool
}

// Row is one derived task.
type Row struct {
	Task      Task
	State     State
	Verdict   string // a label above, "" when the task has no review
	PR        int
	DoneAt    int64
	Recorded  bool // done because master recorded it: a claim, shown with its age
	Note      string
	Reason    string // why blocked, or a note on the state
	WaitingOn []string
	Ask       string // the open ask that makes it wait for the person
	LastFact  int64  // Unix ms of its newest fact, 0 if none
}

// Derive computes every task's row, in file order. Pure: no I/O.
func Derive(f File, in Inputs) []Row {
	rows := map[string]Row{}
	byID := map[string]Task{}
	for _, t := range f.Tasks {
		byID[t.ID] = t
	}
	var get func(id string) Row
	get = func(id string) Row {
		if r, ok := rows[id]; ok {
			return r
		}
		t := byID[id]
		var waiting []string
		for _, a := range t.After {
			if get(a).State != Done {
				waiting = append(waiting, a)
			}
		}
		r := derive(t, in, waiting)
		rows[id] = r
		return r
	}
	out := make([]Row, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		out = append(out, get(t.ID))
	}
	return out
}

func derive(t Task, in Inputs, waitingOn []string) Row {
	r := Row{Task: t, WaitingOn: waitingOn}
	fact := in.Facts[t.ID]
	r.PR, r.Note, r.LastFact = fact.PR, fact.Note, fact.At
	if t.Mission != "" {
		return deriveMission(r, in)
	}
	if in.FactsOK && fact.Done > 0 {
		r.State, r.DoneAt, r.Recorded = Done, fact.Done, true
		return r
	}
	if !in.FactsOK || !in.BoardOK || !in.VerdictsOK || !in.AsksOK {
		r.State = Unknown
		return r
	}
	e, onBoard := in.Board[t.Board]
	if onBoard {
		r.LastFact = max(r.LastFact, boardTime(e))
	}
	review, reviewed := rollUp(t, fact, in)
	if t.Owner != Person && (reviewed || delivered(e) || e.State == "review" || fact.PR > 0) {
		r.Verdict = review
	}
	for _, v := range subjectsVerdicts(t, fact, in) {
		r.LastFact = max(r.LastFact, v.TS)
	}
	ask := openAsk(t.ID, in.Asks)
	if ask != nil {
		r.LastFact = max(r.LastFact, ask.CreatedAt)
	}

	switch {
	case t.Owner == Person && onBoard && e.State == "done":
		r.State = Done
	// Delivery alone is never done: an unreviewed merge:false task waits for
	// master's 'plan done' (the spec's "no verdict and no review set" case
	// cannot be told apart, since the subject defaults to the board task).
	case !t.MergeTask() && t.Owner != Person && delivered(e) && review == Approved:
		r.State = Done
	case ask != nil:
		r.State, r.Ask = Waiting, ask.ID
	case t.MergeTask() && review == Approved:
		r.State = Waiting
	case t.Owner == Person && len(waitingOn) == 0:
		r.State = Waiting
	case onBoard && (e.State == "blocked" || (e.Request != nil && e.Request.BlockedReason != "")):
		r.State = Blocked
		if e.Request != nil {
			r.Reason = e.Request.BlockedReason
		}
	case t.Owner != Person && (delivered(e) || e.State == "review" || fact.PR > 0):
		r.State = InReview
	case onBoard && (e.State == "accepted" || e.State == "working"):
		r.State = InProgress
	default:
		r.State = Upcoming
	}
	return r
}

func deriveMission(r Row, in Inputs) Row {
	if !in.MissionsOK {
		r.State = Unknown
		return r
	}
	s, ok := in.Missions[r.Task.Mission]
	if !ok {
		r.State, r.Reason = Upcoming, "mission not loaded"
		return r
	}
	switch s {
	case mission.Integrated:
		r.State = Done
	case mission.Aborted:
		r.State, r.Reason = Done, "aborted"
	case mission.InReview, mission.Approved, mission.Integrating:
		r.State = InReview
	case mission.Requested, mission.Accepted, mission.Delivered, mission.Checking:
		r.State = InProgress
	case mission.Blocked:
		r.State = Blocked
	default:
		r.State = Upcoming
	}
	return r
}

// delivered: the board request is done, i.e. delivered by its owner, which
// is not finished (review, PR and merge come after).
func delivered(e bus.BoardEntry) bool {
	return e.State == "done"
}

func boardTime(e bus.BoardEntry) int64 {
	t := e.Updated * 1000
	if q := e.Request; q != nil {
		t = max(t, q.CreatedAt, q.AcceptedAt, q.RespondedAt)
	}
	return t
}

// rollUp is bus.RollUp over the union of the task's subjects' verdicts in
// ledger order: its review subject (default its board task) and pr:<n>.
func rollUp(t Task, fact Fact, in Inputs) (string, bool) {
	vs := subjectsVerdicts(t, fact, in)
	if len(vs) == 0 {
		return NoVerdict, false
	}
	switch s, _ := bus.RollUp(vs); s {
	case "APPROVED":
		return Approved, true
	case "REJECTED":
		return ChangesAsked, true
	default:
		return Pending, true
	}
}

// Subjects are the verdict subjects a task's review reads.
func Subjects(t Task, fact Fact) []string {
	var out []string
	if s := t.ReviewSubject(); s != "" {
		out = append(out, s)
	}
	if fact.PR > 0 {
		out = append(out, "pr:"+strconv.Itoa(fact.PR))
	}
	return out
}

func subjectsVerdicts(t Task, fact Fact, in Inputs) []bus.Verdict {
	var vs []bus.Verdict
	for _, s := range Subjects(t, fact) {
		vs = append(vs, in.Verdicts[s]...)
	}
	sort.SliceStable(vs, func(i, j int) bool { return IDLess(vs[i].ID, vs[j].ID) })
	return vs
}

func openAsk(id string, asks []bus.Ask) *bus.Ask {
	for i := range asks {
		if asks[i].Task == id && asks[i].State == "open" {
			return &asks[i]
		}
	}
	return nil
}

// IDLess orders stream ids (<ms>-<seq>) numerically.
func IDLess(a, b string) bool {
	am, as := splitID(a)
	bm, bs := splitID(b)
	if am != bm {
		return am < bm
	}
	return as < bs
}

func splitID(id string) (int64, int64) {
	m, s, _ := strings.Cut(id, "-")
	ms, _ := strconv.ParseInt(m, 10, 64)
	seq, _ := strconv.ParseInt(s, 10, 64)
	return ms, seq
}
