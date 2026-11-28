// Package attention is what waits on the person, from broker state alone
// (status-summary spec §3.2): the ranks of busmon's For you that need no
// busmon's local marks. busmon's For you and agentbus summary both call
// Items, so "waiting on the person" has one definition. The two ranks that
// end on one busmon's local marks (a report that needs the person, an unread
// line) stay in busmon; their numbers are kept here so the list sorts as one.
package attention

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// The ranks, in For you's order; within a rank the oldest comes first.
const (
	RankAsk    = iota // an open ask, one row per ask
	RankHealth        // the stall guard: the team waits on the person, or the person's focus holds a hand-off
	RankWait          // a waiting agent with no open ask, its client's prompt, or a stuck screen
	RankGate          // a pending verdict: an open 4-eyes gate that concerns the person
	RankReport        // busmon's: a report that needs the person (ended by a local mark)
	RankMerge         // a change waiting for the person's merge
	RankUnread        // busmon's: an Activity line not opened yet (a local mark)
)

// The kinds of RankWait, so a reader can tell them apart without the text.
const (
	KindWait   = "wait"   // a declared wait no person answered, no open ask
	KindStuck  = "stuck"  // its screen is unknown and unchanged for StuckAfter
	KindPrompt = "prompt" // its client asks something in its terminal
)

// MergeUnknownKey is the one merge row that says the queue cannot be read:
// never a decision, never opened.
const MergeUnknownKey = "merge:unknown"

// Item is one thing waiting on the person.
type Item struct {
	Rank   int
	Kind   string // RankWait's kind; "" for the other ranks
	Key    string // stable across reads: a new episode is a new key
	Agent  string
	Text   string // "" for KindStuck and KindPrompt: their words are the reader's (busmon's age, the local trust folder)
	At     time.Time
	AskID  string // RankAsk
	Count  int    // RankAsk: its open questions
	By     string // RankGate: the challenger (the gated agent is Agent)
	Head   string // RankMerge: the item's head
	Branch string // RankMerge: the item's branch
}

// Agent is one agent's facts as read from the broker: its declared state
// ({p}:agents), its open ask, when a person last answered its wait
// (bus.WaitAnswered), the dispatcher's observation of it ({p}:observed,
// through Observe) and the gates on it.
type Agent struct {
	State      string
	Message    string
	StateAt    time.Time
	OpenAsk    int       // its open questions
	AnsweredAt time.Time // a person's answer to its wait (bus.WaitAnswered); zero: none
	Obs        Obs
	// StuckAfter is dispatch.stuck_after; 0: a stuck screen is never judged.
	StuckAfter time.Duration
	Gates      map[string]string // ref → gate meta (bus.ParseGateMeta)
	// JudgedAt is the time its observation is judged at, when its reader
	// stamped one (busmon's snapshot); zero: Items' now.
	JudgedAt time.Time
}

// clock is the time a's observation is judged at.
func (a Agent) clock(now time.Time) time.Time {
	if !a.JudgedAt.IsZero() {
		return a.JudgedAt
	}
	return now
}

// Obs is the dispatcher's observation of an agent, as the judgements read it.
type Obs struct {
	State       string    // the reading's state: blocked, unknown, idle…
	OwnPID      bool      // the reading is of the agent's current process
	Prompt      bool      // its own process blocked on its client's prompt
	At          time.Time // when it was read; zero: no reading
	Since       time.Time // since when the state holds
	ScreenSince time.Time // since when its screen has not changed
}

// Observe is an agent's Obs from its observation (ok: one was read) and the
// pid its snapshot records: a reading carried for another process (a
// replaced agent) is never its prompt.
func Observe(o bus.Observation, ok bool, pid int) Obs {
	var r Obs
	if !ok {
		return r
	}
	r.State = o.State
	if o.At > 0 {
		r.At = time.UnixMilli(o.At)
	}
	r.Since = r.At // a dispatcher before v0.15 writes no since
	if o.Since > 0 {
		r.Since = time.UnixMilli(o.Since)
	}
	r.OwnPID = o.PID != 0 && o.PID == pid
	r.Prompt = r.OwnPID && o.State == "blocked"
	if o.ScreenSince > 0 {
		r.ScreenSince = time.UnixMilli(o.ScreenSince)
	}
	return r
}

// Fresh: the observation is younger than its TTL at now. It is judged when
// read, so a reader whose reads fail stops showing a prompt once the reading
// it holds is too old.
func (o Obs) Fresh(now time.Time) bool {
	return !o.At.IsZero() && now.Sub(o.At) < time.Duration(bus.ObservedTTL)*time.Millisecond
}

// Answered: a declared wait a person has replied to since it was declared,
// with no open question left.
func Answered(a Agent) bool {
	return a.State == "waiting" && a.OpenAsk == 0 && !a.AnsweredAt.IsZero() && a.AnsweredAt.After(a.StateAt)
}

// PromptBlocked (v0.15 §13, agent-screen-blocked §3): the agent's own client
// asks something in its terminal, seen by the dispatcher, while the agent
// declares neither blocked nor waiting and has no open ask. Only a person can
// answer it, there.
func PromptBlocked(a Agent, now time.Time) bool {
	return a.Obs.Prompt && a.Obs.Fresh(now) && a.State != "blocked" && a.State != "waiting" && a.OpenAsk == 0
}

// ScreenStuck (agent-screen-blocked §3): the observation of the agent's
// current process reads its screen as unknown and that screen has not
// changed for StuckAfter; the agent declares neither blocked nor waiting and
// has no open ask.
func ScreenStuck(a Agent, now time.Time) bool {
	o := a.Obs
	return a.StuckAfter > 0 && o.OwnPID && o.State == "unknown" && !o.ScreenSince.IsZero() &&
		o.Fresh(now) && o.At.Sub(o.ScreenSince) >= a.StuckAfter &&
		a.State != "blocked" && a.State != "waiting" && a.OpenAsk == 0
}

// Inputs are every read the ranks need; nothing else is read.
type Inputs struct {
	// Person is the person's identity; "" when there is none (busmon's
	// default identity is none: no gate is ever the person's then).
	Person string
	Asks   []bus.Ask        // {p}:asks
	Agents map[string]Agent // {p}:agents with their observations and gates
	// PersonGates are the gates on the person, when the person is no agent.
	PersonGates map[string]string
	// Health are the health records; HealthNow is the broker's clock they
	// are judged at (the stall guard).
	Health    map[string]health.Record
	HealthNow time.Time
	// Dispatch is the dispatch service's state ("", up, down, stopped…),
	// for the merge queue's unknown row.
	Dispatch string
	// MergeRead: the merge queue was read at all (a reader exists);
	// MergeErr is why the last read failed, "" when it did not.
	MergeRead bool
	Merge     bus.MergeQueueRecord
	MergeErr  string
}

// Items are the broker-only items, ranked, oldest first within a rank. now
// judges the observations' freshness, for an agent with no JudgedAt.
func Items(in Inputs, now time.Time) []Item {
	var out []Item
	for _, a := range OpenAsks(in.Asks) {
		if a.Open() == 0 || a.CreatedAt <= 0 {
			continue
		}
		out = append(out, Item{Rank: RankAsk, Key: "ask:" + a.ID, Agent: a.From, Text: a.Title,
			At: time.UnixMilli(a.CreatedAt), AskID: a.ID, Count: a.Open()})
	}
	for name, a := range in.Agents {
		switch {
		case a.State == "waiting" && !Answered(a) && a.OpenAsk == 0:
			out = append(out, Item{Rank: RankWait, Kind: KindWait, Key: fmt.Sprintf("wait:%s:%d", name, a.StateAt.UnixMilli()),
				Agent: name, Text: a.Message, At: a.StateAt})
		case ScreenStuck(a, a.clock(now)):
			out = append(out, Item{Rank: RankWait, Kind: KindStuck, Key: fmt.Sprintf("stuck:%s:%d", name, a.Obs.ScreenSince.UnixMilli()),
				Agent: name, At: a.Obs.ScreenSince})
		case PromptBlocked(a, a.clock(now)):
			since := a.Obs.Since
			if since.IsZero() {
				since = a.Obs.At
			}
			out = append(out, Item{Rank: RankWait, Kind: KindPrompt, Key: fmt.Sprintf("prompt:%s:%d", name, since.UnixMilli()),
				Agent: name, At: since})
		}
		out = append(out, gateItems(in.Person, name, a.Gates)...)
	}
	if _, isAgent := in.Agents[in.Person]; !isAgent {
		out = append(out, gateItems(in.Person, in.Person, in.PersonGates)...)
	}
	out = append(out, stallItems(in.Health, in.HealthNow)...)
	out = append(out, mergeItems(in)...)
	Sort(out)
	return out
}

// Sort orders items by rank, then the dated before the undated (a gate whose
// ref is not a stream id), the oldest first, then by key.
func Sort(out []Item) {
	sort.SliceStable(out, func(i, j int) bool { return Less(out[i], out[j]) })
}

// Less is Sort's order, for a reader that sorts its own items with these.
func Less(a, b Item) bool {
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	if a.At.IsZero() != b.At.IsZero() {
		return b.At.IsZero()
	}
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	return a.Key < b.Key
}

// OpenAsks are the asks still open, oldest first.
func OpenAsks(asks []bus.Ask) []bus.Ask {
	var out []bus.Ask
	for _, a := range asks {
		if a.State == "open" {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// stallItems are the stall guard's rows for the person (spec
// 2026-11-01-stall-guard-design.md): team-stall waiting on the person's own
// task, and clear-stall held by the person's focused pane. Only a current
// warn with that reason; the key carries the episode's since, so a new
// episode is a new row.
func stallItems(records map[string]health.Record, now time.Time) []Item {
	var out []Item
	for _, c := range []struct{ id, reason, word string }{
		{"team-stall", "waiting-person", "stall"},
		{"clear-stall", "pane-focused", "clear"},
	} {
		d, ok := health.ByID(c.id)
		rec, has := records[c.id]
		if !ok || !has || rec.Reason != c.reason || health.EffectiveStatus(rec, now.UnixMilli(), d.Interval) != health.Warn {
			continue
		}
		out = append(out, Item{Rank: RankHealth, Key: fmt.Sprintf("health:%s:%d", c.id, rec.Since),
			Agent: c.word, Text: rec.Detail, At: time.UnixMilli(rec.Since)})
	}
	return out
}

// gateItems are the gates on gated that concern the person (foryou-gates): a
// gate the person opened or one holding the person. A review between agents
// is theirs to finish, never a row.
func gateItems(person, gated string, gates map[string]string) []Item {
	if person == "" {
		return nil
	}
	var out []Item
	for ref, meta := range gates {
		g := bus.ParseGateMeta(meta)
		if g.Challenger != person && gated != person {
			continue
		}
		summary := g.Text
		if summary == "" {
			summary = ref
		}
		out = append(out, Item{Rank: RankGate, Key: "gate:" + gated + ":" + ref, Agent: gated, By: g.Challenger,
			Text: summary, At: bus.IDTime(ref)})
	}
	return out
}

// mergeItems are the merge queue's rows (merge-queue spec §4): one per item,
// dated by its deciding approve and marked "(as of HH:MM: <reason>)" while
// the last pass failed; with no items known and the queue unknown (never
// computed, a failed read, or a failed pass even after an empty success),
// one row that says why. Nothing when the queue is not read at all.
func mergeItems(in Inputs) []Item {
	if !in.MergeRead {
		return nil
	}
	rec := in.Merge
	why := in.MergeErr
	if why == "" {
		why = rec.Reason
	}
	unknown := func(text string) []Item {
		return []Item{{Rank: RankMerge, Key: MergeUnknownKey, Agent: "you", Text: text}}
	}
	switch {
	case len(rec.Items) == 0 && why != "":
		return unknown("merge queue unknown: " + why)
	case rec.ItemsAt == 0 && DispatchWhy(in.Dispatch) != "":
		return unknown("merge queue unknown: " + DispatchWhy(in.Dispatch))
	case rec.ItemsAt == 0:
		return unknown("merge queue not computed yet (the dispatch service computes it)")
	}
	var out []Item
	for _, it := range rec.Items {
		text := "merge " + it.Branch
		if it.Number > 0 {
			text = fmt.Sprintf("merge PR %d", it.Number)
			if it.Title != "" {
				text += ": " + it.Title
			}
		}
		if OneApproval(it) {
			text += " (1 approval)"
		}
		if why != "" {
			text += " (as of " + time.UnixMilli(rec.ItemsAt).Local().Format("15:04") + ": " + why + ")"
		}
		out = append(out, Item{Rank: RankMerge, Key: MergeKey(it), Agent: "you", Text: text,
			At: time.UnixMilli(it.ApprovedAt), Head: it.Head, Branch: it.Branch})
	}
	return out
}

// DispatchWhy is why the dispatch service computes no merge queue: stopped,
// down, failed or gave up; "" otherwise.
func DispatchWhy(dispatch string) string {
	switch dispatch {
	case "stopped":
		return "project stopped"
	case "down", "failed", "gave-up":
		return "dispatch " + dispatch
	}
	return ""
}

// OneApproval: the item's head has one current approver (single-approval
// §4.5); never read from len(Approvals), which counts stale and repeated
// approves too. A record written before Approvers says nothing.
func OneApproval(it bus.MergeItem) bool {
	return len(it.Approvers) == 1
}

// MergeKey is a merge item's key (triage-dedupe §2): its PR's number, so a
// rebase keeps it; its branch when it has none.
func MergeKey(it bus.MergeItem) string {
	if it.Number > 0 {
		return "merge:pr:" + strconv.Itoa(it.Number)
	}
	return "merge:branch:" + it.Branch
}
