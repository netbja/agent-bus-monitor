package main

import (
	"github.com/netbja/agent-bus-monitor/bus"
)

// Unread marks (busmon UX spec, item 8): an Activity entry addressed to the
// person carries a mark until the person opens it. The state is the person's,
// kept in the local cache, never on the bus. Everything here runs on the UI
// goroutine.

// mark is an Activity line's unread state.
type mark int

const (
	markNone       mark = iota // not for the person, opened, or before the last visit
	markUnread                 // for the person and not opened
	markSuperseded             // not opened, but a newer one, an answer or a closed ask made it moot
)

// unreadState: which entries (by stream id) are for the person, which the
// person opened, and the floor (the last visit, Unix ms) below which nothing
// is marked, so a first launch does not mark the whole backfill.
type unreadState struct {
	forPerson map[string]bool
	read      map[string]bool
	floor     int64
}

func newUnreadState() unreadState {
	return unreadState{forPerson: map[string]bool{}, read: map[string]bool{}}
}

// personEntry reports whether an entry is addressed to the person, from its
// own fields: an ask, a cmd to the person's name (never to the default
// identity "pilot", a bus word), or a dispatcher notice marked for=person.
// A report is for the person only through its agent's wait (classifyWaits).
func personEntry(e bus.Event, person string) bool {
	switch e.Kind {
	case "cmd":
		if e.Type == bus.CmdAsk {
			return true
		}
		return !e.Human && person != "" && person != defaultHuman && e.Target == person
	case "notify":
		return e.For == bus.ForPerson
	}
	return false
}

// sender is who wrote an entry.
func sender(e bus.Event) string {
	if e.From != "" {
		return e.From
	}
	return e.Agent
}

// marks computes every entry's mark, by stream id. A newer entry for the
// person from the same sender supersedes an older one, as does an answer from
// the person (waitingAnswerAt) or, for an ask, its closing.
func (s unreadState) marks(feed []feedLine, asks map[string]bus.Ask) map[string]mark {
	out := map[string]mark{}
	events := make([]bus.Event, 0, len(feed))
	for _, fl := range feed {
		if !fl.sep {
			events = append(events, fl.ev)
		}
	}
	newer := map[string]bool{}
	for i := len(feed) - 1; i >= 0; i-- {
		fl := feed[i]
		if fl.sep || !s.forPerson[fl.ev.ID] {
			continue
		}
		from := sender(fl.ev)
		superseded := newer[from]
		newer[from] = true
		if s.read[fl.ev.ID] || fl.at.UnixMilli() < s.floor {
			continue
		}
		a, isAsk := asks[fl.ev.ID]
		switch {
		case superseded, isAsk && a.Open() == 0, !waitingAnswerAt(from, fl.at, events).IsZero():
			out[fl.ev.ID] = markSuperseded
		default:
			out[fl.ev.ID] = markUnread
		}
	}
	return out
}

func unreadCount(m map[string]mark) int {
	n := 0
	for _, v := range m {
		if v == markUnread {
			n++
		}
	}
	return n
}

// waitingNames are the agents that wait for the person or carry the hint.
func waitingNames(agents map[string]*agentState) []string {
	var waiting []string
	for name, a := range agents {
		if waitsForPerson(a) || a.forYou {
			waiting = append(waiting, name)
		}
	}
	return waiting
}

func (f *activityFeed) classifyWaits(waiting []string) {
	for _, name := range waiting {
		if id := lastReportID(name, f.feed); id != "" {
			f.unread.forPerson[id] = true
		}
	}
}

// lastReportID is the stream id of the agent's newest report in the feed.
func lastReportID(name string, feed []feedLine) string {
	for i := len(feed) - 1; i >= 0; i-- {
		if e := feed[i].ev; !feed[i].sep && e.Kind == "report" && e.Agent == name {
			return e.ID
		}
	}
	return ""
}
