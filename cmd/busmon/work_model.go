package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The Work model (requests-plan-redesign, the person's answer of 2026-11-10):
// one producer for who owes what. The Work page (layout A), the Kanban
// (layout B), the header's strip and the For you panel's label all read it,
// so they show one count.
//
// Where an item goes, written down (correction 2):
//   - YOU: what waits on the person. An open ask, a waiting agent or its
//     client's prompt, and a request or directive owed by the person: answer.
//     A change both reviewers approved on its pushed head (the merge queue):
//     merge. A plan task of the person's that nothing blocks: decide. A gate
//     that concerns the person: review. The stall guard: check.
//   - NEXT: the plan's tasks not started, in file order, the person's tasks
//     blocked by other tasks among them; then the tracked requests no plan
//     task names (not in the plan) and not owed by the person (those are YOU).
//   - IDEAS: the open questions recorded in the repository
//     (docs/memory/open-questions, status proposed or active) that no plan
//     task covers; ◇ is theirs alone.
//   - TEAM: one row per agent at work (working, or holding a task or a
//     review gate), with its task in hand, the gates it holds as a reviewer,
//     and the reviewers of its own work; the free agents (idle, done) on one
//     line, as they can take a request.

// Verbs, the same on every surface (correction 1).
const (
	verbAnswer = "answer"
	verbMerge  = "merge"
	verbDecide = "decide"
	verbReview = "review"
	verbCheck  = "check"
)

// workItem is a row of YOU or NEXT.
type workItem struct {
	Verb  string // YOU's verb; "" in NEXT
	Key   string // the For you key, a Requests thread key (req:<task>, thr:<id>), or plan:<task>
	Who   string // the asker, the owner, plan.Person for the person
	Text  string
	At    time.Time
	Plan  string   // the plan task's id, when one names it
	Req   *request // a tracked request: + and - act on it
	After []string // NEXT: the tasks it waits on
}

// teamRow is an agent at work.
type teamRow struct {
	Agent     string
	Word      string   // its state's word (stateLook)
	Task      *request // its accepted tracked request, nil when none
	Plan      string   // the plan task naming that request
	Message   string   // its status message
	Reviewing []teamGate
	Reviewers []teamGate // gates opened by this agent: who reviews its work
}

// teamGate is an open 4-eyes gate: the other side and since when.
type teamGate struct {
	Who, Subject string
	Since        time.Time
}

// workIdea is a row of IDEAS.
type workIdea struct {
	ID, Title, Where string
}

// workModel is the computed page.
type workModel struct {
	You      []workItem
	Team     []teamRow
	Idle     []string
	Next     []workItem
	Ideas    []workIdea
	IdeasErr bool // the notes could not be read: the page says so, never "none"
	Between  int  // threads between agents nobody waits on the person for
	Done     int  // plan tasks done
	Planned  bool // a plan is loaded
}

// youCount is the one counter (correction 3).
func (w workModel) youCount() int { return len(w.You) }

// strip is the header's chips from the model: merge, and decide for every
// other YOU row, so decide + merge is YOU's count.
func (w workModel) strip(unread int) queueStrip {
	q := queueStrip{unread: unread}
	for _, it := range w.You {
		if it.Verb == verbMerge {
			q.merge++
		} else {
			q.decide++
		}
	}
	return q
}

// workInput is what the model reads, all of it already in busmon.
type workInput struct {
	forYou   []attentionItem
	reqs     reqView
	requests []request
	plan     planState
	agents   map[string]*agentState
	pilot    string
	ideas    []memory.Question
	ideasErr bool
	now      time.Time
}

// workVerbOf is a For you rank's verb.
func workVerbOf(it attentionItem) string {
	switch it.Rank {
	case rankMerge:
		return verbMerge
	case rankGate:
		return verbReview
	case rankHealth:
		return verbCheck
	}
	return verbAnswer
}

func workOf(in workInput) workModel {
	var w workModel
	for _, it := range in.forYou {
		if it.Rank == rankUnread || it.Key == mergeUnknownKey {
			continue
		}
		text := it.Text
		if it.Rank == rankAsk && it.Count > 1 { // as For you's row says it
			text += fmt.Sprintf(" (%d questions)", it.Count)
		}
		w.You = append(w.You, workItem{Verb: workVerbOf(it), Key: it.Key, Who: it.Agent, Text: text, At: it.At})
	}
	for _, g := range in.reqs.groups[secOwed] {
		for _, th := range g.Threads {
			if th.Kind == kindChallenge { // a gate on the person: For you's review row
				continue
			}
			w.You = append(w.You, workItem{Verb: verbAnswer, Key: threadKey(th), Who: th.From, Text: th.Subject, At: th.At, Req: th.Request})
		}
	}
	links := planLinks(in.plan)
	owed := map[string]bool{} // board tasks the person owes: their answer row stands for their plan task
	for _, it := range w.You {
		if it.Req != nil {
			owed[it.Req.Task] = true
		}
	}
	w.Planned = in.plan.loaded
	if in.plan.loaded {
		for _, r := range in.plan.rows {
			switch {
			case r.Task.Board != "" && owed[r.Task.Board] && r.State != plan.Done:
				// listed once, as the request the person answers (reviewer-codex)
			case r.State == plan.Done:
				w.Done++
			case r.Task.Owner == plan.Person && r.State == plan.Waiting && r.Ask == "":
				w.You = append(w.You, workItem{Verb: verbDecide, Key: "plan:" + r.Task.ID, Who: plan.Person,
					Text: r.Task.Title, At: msTime(r.LastFact), Plan: r.Task.ID})
			case r.State == plan.Upcoming:
				w.Next = append(w.Next, workItem{Key: "plan:" + r.Task.ID, Who: r.Task.Owner, Text: r.Task.Title,
					Plan: r.Task.ID, After: r.WaitingOn, Req: boardRequest(in.requests, r.Task.Board)})
			}
		}
	}
	for i := range in.requests {
		r := in.requests[i]
		if !r.Tracked || r.State != "requested" {
			continue
		}
		if in.reqs.self != "" && target(r) == in.reqs.self { // owed by the person: YOU's answer row (reviewer-codex)
			continue
		}
		if _, named := links[r.Task]; named {
			continue
		}
		at := r.Created
		if at.IsZero() {
			at = r.Updated
		}
		w.Next = append(w.Next, workItem{Key: "req:" + r.Task, Who: target(r), Text: r.Task, At: at, Req: &r})
	}
	w.Between = in.reqs.count(secTracked) + in.reqs.count(secReviews) + in.reqs.count(secDirectives)
	w.Team, w.Idle = teamOf(in, links)
	w.IdeasErr = in.ideasErr
	for _, q := range in.ideas {
		if !ideaPlanned(q, in.plan) {
			w.Ideas = append(w.Ideas, workIdea{ID: q.ID, Title: q.Title, Where: q.Path})
		}
	}
	return w
}

// boardRequest is the tracked request on a board task, nil when none.
func boardRequest(reqs []request, task string) *request {
	if task == "" {
		return nil
	}
	for i := range reqs {
		if reqs[i].Tracked && reqs[i].Task == task {
			r := reqs[i]
			return &r
		}
	}
	return nil
}

// teamOf is TEAM: the agents at work, then the idle ones' names. The person
// and an agent never seen are left out.
func teamOf(in workInput, links map[string]planLink) ([]teamRow, []string) {
	names, _ := visibleAgents(in.agents, in.pilot, false, in.now)
	reviewers := map[string][]teamGate{} // challenger -> its reviewers
	reviewing := map[string][]teamGate{}
	for _, n := range names {
		for ref, meta := range in.agents[n].gates {
			g := bus.ParseGateMeta(meta)
			subject := g.Subject
			if subject == "" {
				subject = oneLine(g.Text)
			}
			since := eventTimestamp(ref)
			reviewing[n] = append(reviewing[n], teamGate{Who: g.Challenger, Subject: subject, Since: since})
			if g.Challenger != "" {
				reviewers[g.Challenger] = append(reviewers[g.Challenger], teamGate{Who: n, Subject: subject, Since: since})
			}
		}
	}
	var team []teamRow
	var idle []string
	for _, n := range names {
		a := in.agents[n]
		_, word, _, _ := stateLook(a)
		row := teamRow{Agent: n, Word: word, Message: a.message, Reviewing: sortGates(reviewing[n]), Reviewers: sortGates(reviewers[n])}
		for i := range in.requests {
			r := in.requests[i]
			if r.Tracked && r.State == "accepted" && reqOwner(r) == n {
				row.Task = &r
				row.Plan = links[r.Task].id
				break
			}
		}
		free := word == "idle" || word == "done" || word == "answered"
		switch {
		case word != "working" && !free:
			// waiting, blocked, held or paused: neither, whatever it holds
			// (the Agents panel says which; a wait on the person is YOU's;
			// reviewer-codex)
		case word == "working" || row.Task != nil || len(row.Reviewing) > 0:
			team = append(team, row) // at work
		default:
			idle = append(idle, n) // free: it can take a request
		}
	}
	return team, idle
}

func sortGates(gs []teamGate) []teamGate {
	sort.Slice(gs, func(i, j int) bool {
		if !gs[i].Since.Equal(gs[j].Since) {
			return gs[i].Since.Before(gs[j].Since)
		}
		return gs[i].Who < gs[j].Who
	})
	return gs
}

// ideaWords are a title's words, lowercased, for the dedupe.
var ideaWords = regexp.MustCompile(`[a-z0-9]+`)

func ideaKey(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "Decide:"); ok {
		s = rest
	}
	return strings.Join(ideaWords.FindAllString(strings.ToLower(s), -1), " ")
}

// ideaPlanned says a plan task covers the note (Q2: a note never shows twice):
// a task whose board or id names the note's id, or whose title, without
// "Decide:", is the note's title.
func ideaPlanned(q memory.Question, p planState) bool {
	if !p.loaded {
		return false
	}
	key := ideaKey(q.Title)
	for _, r := range p.rows {
		if r.Task.Board == q.ID || r.Task.ID == q.ID || (key != "" && ideaKey(r.Task.Title) == key) {
			return true
		}
	}
	return false
}
