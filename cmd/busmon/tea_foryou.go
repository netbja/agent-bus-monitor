package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// For you (busmon Charm spec, decision 2): one list of what waits on the
// person, ranked, oldest first within a rank. Every other surface is a named
// subset of it (decisions, unreadItems), so they can never disagree. The live
// tview reminder keeps attentionItems until step c.

const (
	rankAsk    = iota // an open ask, one row per ask
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
		// A gate holds Agent until a verdict; its challenger opened it. Who
		// reviews whom depends on who challenged (master gates a reviewer, a
		// reviewer gates an author), so the row names both, neutrally.
		for ref, meta := range a.gates {
			by, summary, _ := strings.Cut(meta, "|")
			if summary == "" {
				summary = ref
			}
			out = append(out, attentionItem{Rank: rankGate, Key: "gate:" + name + ":" + ref, Agent: name, By: by, Text: summary,
				At: eventTimestamp(ref)})
		}
	}
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
