package main

import (
	"fmt"
	"github.com/netbja/agent-bus-monitor/plan"
	"sort"
	"strings"
	"time"

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
		out = append(out, attentionItem{Rank: rankUnread, Key: "unread:" + fl.ev.ID, Agent: sender(fl.ev),
			Text: fl.ev.Message, At: fl.at, EventID: fl.ev.ID})
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
		if it.Rank != rankUnread {
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

// forYouEmpty is For you when its list is empty (the person: "I must always
// know what is expected"). It says "Nothing expected from you." only when
// nothing else is owed either: no request owed to the person (owed, the
// Requests view's count, as the header's orange item) and no plan task of the
// person's waiting; otherwise it names those (foureyes). Then what is in
// progress (the agents that declare working, with their status) and the next
// decision to come (the plan's first person task Blocked or Upcoming), from
// what busmon already reads. No store of its own.
func forYouEmpty(s snapshot, owed int) []Line {
	var working []string
	names := make([]string, 0, len(s.agents))
	for n := range s.agents {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if a := s.agents[n]; a.state == "working" {
			w := n
			if a.message != "" {
				w += " " + GlyphNone + " " + a.message
			}
			working = append(working, w)
		}
	}
	inProgress := "nobody declares working"
	if len(working) > 0 {
		inProgress = strings.Join(working, "; ")
	}
	waiting, next := "", "no decision planned"
	if s.planState.loaded {
		for _, r := range s.planState.rows {
			if r.Task.Owner == plan.Person && r.State == plan.Waiting && waiting == "" {
				waiting = r.Task.Title
			}
		}
		for _, st := range []plan.State{plan.Blocked, plan.Upcoming} {
			for _, r := range s.planState.rows {
				if r.Task.Owner == plan.Person && r.State == st && next == "no decision planned" {
					next = r.Task.Title
				}
			}
		}
	}
	var lines []Line
	if owed > 0 {
		word := "requests"
		if owed == 1 {
			word = "request"
		}
		lines = append(lines, Line{{Text: fmt.Sprintf("Owed by you: %d %s (Requests)", owed, word), Role: RoleAttention}})
	}
	if waiting != "" {
		lines = append(lines, Line{{Text: "Waiting for you: ", Role: RoleAttention}, {Text: Preview(waiting), Role: RoleText}})
	}
	if len(lines) == 0 {
		lines = append(lines, Line{{Text: "Nothing expected from you.", Role: RoleOK}})
	}
	return append(lines,
		Line{{Text: "In progress: ", Role: RoleMuted}, {Text: Preview(inProgress), Role: RoleText}},
		Line{{Text: "Next for you: ", Role: RoleMuted}, {Text: Preview(next), Role: RoleText}})
}
