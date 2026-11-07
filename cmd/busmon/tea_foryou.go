package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// For you (busmon Charm spec, decision 2): one list of what waits on the
// person, ranked, oldest first within a rank. Every other surface is a named
// subset of it (decisions, unreadItems), so they can never disagree.

const (
	rankAsk    = iota // an open ask, one row per ask
	rankHealth        // the stall guard: the team waits on the person, or the person's focus holds a hand-off
	rankWait          // a waiting agent with no open ask, or its client's prompt
	rankGate          // a pending verdict: an open 4-eyes gate
	rankMerge         // a change waiting for the person's merge (merge-queue): Enter opens its page
	rankUnread        // an Activity line for the person not opened yet
)

// forYou is the list, from the snapshot and the feed's marks. The "for you"
// hint (forYouHint) is not a rank: it stays a flag on the agent's row.
func forYou(s snapshot, feed []feedLine) []attentionItem {
	var out []attentionItem
	for _, a := range openAsks(s.asks) {
		if a.Open() == 0 || a.CreatedAt <= 0 {
			continue
		}
		out = append(out, attentionItem{Rank: rankAsk, Key: "ask:" + a.ID, Agent: a.From, Text: a.Title,
			At: msTime(a.CreatedAt), AskID: a.ID, Count: a.Open()})
	}
	for name, a := range s.agents {
		switch {
		case a.state == "waiting" && !answered(a) && a.openAsk == 0:
			out = append(out, attentionItem{Rank: rankWait, Key: fmt.Sprintf("wait:%s:%d", name, a.stateAt.UnixMilli()),
				Agent: name, Text: a.message, At: a.stateAt})
		case promptWaiting(a):
			since := a.obsSince
			if since.IsZero() {
				since = a.obsAt
			}
			out = append(out, attentionItem{Rank: rankWait, Key: fmt.Sprintf("prompt:%s:%d", name, since.UnixMilli()),
				Agent: name, Text: "pane prompt", At: since})
		}
		out = append(out, personGateItems(s.person, name, a.gates)...)
	}
	if _, isAgent := s.agents[s.person]; !isAgent {
		out = append(out, personGateItems(s.person, s.person, s.personGates)...)
	}
	out = append(out, stallItems(s.guardianHealth, s.guardianNow)...)
	out = append(out, mergeItems(s)...)
	asks := map[string]int{} // an open ask's row, by ask id (the id of its Activity line)
	for i, it := range out {
		if it.Rank == rankAsk {
			asks[it.AskID] = i
		}
	}
	for _, fl := range feed {
		if fl.sep || fl.mark != markUnread {
			continue
		}
		if i, ok := asks[fl.ev.ID]; ok && fl.ev.Type == bus.CmdAsk {
			out[i].Unread = true // listed once: the ask's row carries the mark
			continue
		}
		text := fl.ev.Message
		if len(fl.personNotices) > 0 || fl.ev.Kind == "report" {
			text = lineHeadline(fl.ev) // a report notice reads as its report's headline (report-one-row §4)
		} else if s, _, ok := reportSubject(text); ok {
			text = s
		}
		ids := []string{fl.ev.ID}
		if len(fl.personNotices) > 0 { // one row per report, holding every notice to the person (§3)
			ids = fl.personNotices
		}
		out = append(out, attentionItem{Rank: rankUnread, Key: unreadKey(fl), Agent: sender(fl.ev),
			Text: text, At: fl.at, EventID: ids[0], IDs: ids})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.At.IsZero() != b.At.IsZero() { // an undated item (a gate whose ref is not a stream id) last
			return b.At.IsZero()
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.Key < b.Key
	})
	return out
}

// stallItems are the stall guard's rows for the person (spec
// 2026-11-01-stall-guard-design.md, busmon): team-stall waiting on the
// person's own task, and clear-stall held by the person's focused pane. Only
// a current warn with that reason; the key carries the episode's since, so a
// new episode is a new row.
func stallItems(records map[string]guardian.Record, now time.Time) []attentionItem {
	var out []attentionItem
	for _, c := range []struct{ id, reason, word string }{
		{"team-stall", "waiting-person", "stall"},
		{"clear-stall", "pane-focused", "clear"},
	} {
		d, ok := guardian.ByID(c.id)
		rec, has := records[c.id]
		if !ok || !has || rec.Reason != c.reason || guardian.EffectiveStatus(rec, now.UnixMilli(), d.Interval) != guardian.Warn {
			continue
		}
		out = append(out, attentionItem{Rank: rankHealth, Key: fmt.Sprintf("health:%s:%d", c.id, rec.Since),
			Agent: c.word, Text: rec.Detail, At: msTime(rec.Since)})
	}
	return out
}

// personGateItems are the rows of the gates on gated that concern the person
// (foryou-gates): a gate the person opened (they asked for that review) or
// one holding the person. A review between agents is theirs to finish, never
// a row: master's challenges to reviewers stay off the list and the badge.
// The default identity is no person, so it never matches. Who reviews whom
// depends on who challenged, so the row names both, neutrally.
func personGateItems(person, gated string, gates map[string]string) []attentionItem {
	if person == "" || person == defaultHuman {
		return nil
	}
	var out []attentionItem
	for ref, meta := range gates {
		g := bus.ParseGateMeta(meta)
		if g.Challenger != person && gated != person {
			continue
		}
		summary := g.Text
		if summary == "" {
			summary = ref
		}
		out = append(out, attentionItem{Rank: rankGate, Key: "gate:" + gated + ":" + ref, Agent: gated, By: g.Challenger,
			Text: summary, At: eventTimestamp(ref)})
	}
	return out
}

// decisions are the items that wait on a person's decision: asks, waiting
// agents and gates. The header's for-you badge counts them; the reminder lists
// them; never an unread line.
func decisions(items []attentionItem) []attentionItem {
	var out []attentionItem
	for _, it := range items {
		if it.Rank != rankUnread && it.Key != mergeUnknownKey {
			out = append(out, it)
		}
	}
	return out
}

// unreadItems is the header's unread badge: the unread lines of the list,
// an ask's folded line included, so it equals the Activity marks.
func unreadItems(items []attentionItem) int {
	n := 0
	for _, it := range items {
		if it.Rank == rankUnread || it.Unread {
			n++
		}
	}
	return n
}

// planNextPrefix is the plan pointer's key: plan-next:<task> (home-inbox-drawer
// §3.3). Enter opens the task page.
const planNextPrefix = "plan-next:"

// nextPlanTask is the plan pointer's task (§3.3): the first person task, in
// the plan file's order, that is Waiting; with none, the first Blocked, then
// Upcoming. False with none at all, or no plan loaded.
//
// Plan tasks are no For you rows any more (the person's choice, 2026-11-07,
// home-inbox-drawer): a task waiting for the person is counted in the
// header's plan item (planSeg's ♙), not in decide, and this one pointer
// says which comes next. It supersedes "a decision like any other, counted
// in the title and the badge": no "Waiting for you" line remains in For you.
func nextPlanTask(p planState) (plan.Row, bool) {
	if !p.loaded {
		return plan.Row{}, false
	}
	for _, st := range []plan.State{plan.Waiting, plan.Blocked, plan.Upcoming} {
		for _, r := range p.rows {
			if r.Task.Owner == plan.Person && r.State == st {
				return r, true
			}
		}
	}
	return plan.Row{}, false
}

// planPointer is the pointer's row: muted, never counted (§3.3).
func planPointer(s snapshot) (emptyRow, bool) {
	r, ok := nextPlanTask(s.planState)
	if !ok {
		return emptyRow{}, false
	}
	return emptyRow{key: planNextPrefix + r.Task.ID, line: Line{{Text: "Next for you: ", Role: RoleMuted}, {Text: Preview(r.Task.Title), Role: RoleMuted}}}, true
}

// forYouPanelItems is what the For you panel draws: the list minus its
// unread lines (§3.2). They stay in the model: the strip's unread chip, u and
// the walk reach them; an ask's own unread mark stays on its row.
func forYouPanelItems(items []attentionItem) []attentionItem {
	var out []attentionItem
	for _, it := range items {
		if it.Rank != rankUnread {
			out = append(out, it)
		}
	}
	return out
}

// forYouLabel is the panel's frame label: the strip's words, decide and
// merge, each at 0 left out (§3.2), so the panel and the strip never disagree.
func forYouLabel(q queueStrip) string {
	var parts []string
	if q.decide > 0 {
		parts = append(parts, fmt.Sprintf("%d decide", q.decide))
	}
	if q.merge > 0 {
		parts = append(parts, fmt.Sprintf("%d merge", q.merge))
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

// emptyRow is one line of the empty state, selectable when it has a key:
// plan-next:<task> opens the plan task.
type emptyRow struct {
	key  string
	line Line
}

// forYouEmpty is the empty state's lines (the recap and the layout read them).
func forYouEmpty(s snapshot, owed int) []Line {
	var out []Line
	for _, r := range forYouEmptyRows(s, owed) {
		out = append(out, r.line)
	}
	return out
}

// forYouEmptyRows: what is owed, else "Nothing expected", then the plan
// pointer (planPointer, the one producer), if any. What is in progress is the
// Agents panel's: never repeated here (foryou-no-progress, the person's
// answer). A plan task waiting for the person is the pointer, not something
// owed (home-inbox-drawer §3.3).
func forYouEmptyRows(s snapshot, owed int) []emptyRow {
	var rows []emptyRow
	if owed > 0 {
		word := "requests"
		if owed == 1 {
			word = "request"
		}
		rows = append(rows, emptyRow{line: Line{{Text: fmt.Sprintf("Owed by you: %d %s (Requests)", owed, word), Role: RoleAttention}}})
	} else {
		rows = append(rows, emptyRow{line: Line{{Text: "Nothing expected from you.", Role: RoleOK}}})
	}
	if p, ok := planPointer(s); ok {
		rows = append(rows, p)
	}
	return rows
}
