package main

import (
	"slices"

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

// classifyWaits marks the newest report of every agent that waits for the
// person (or carries the "for you" hint) as for the person: it is the text
// the agent detail shows as "What <agent> needs from you".
func (u *ui) classifyWaits() {
	u.st.mu.Lock()
	var waiting []string
	for name, a := range u.st.agents {
		if waitsForPerson(a) || a.forYou {
			waiting = append(waiting, name)
		}
	}
	u.st.mu.Unlock()
	for _, name := range waiting {
		if id := lastReportID(name, u.feed); id != "" {
			u.unread.forPerson[id] = true
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

// markRead records that the person opened an entry, and saves the cache with
// only the ids still in the retained window (the file cannot grow).
func (u *ui) markRead(id string) {
	if !u.unread.forPerson[id] || u.unread.read[id] {
		return
	}
	u.unread.read[id] = true
	if u.cache == nil {
		return
	}
	retained := map[string]bool{}
	for _, fl := range u.feed {
		if !fl.sep {
			retained[fl.ev.ID] = true
		}
	}
	var keep []string
	for _, r := range append(slices.Clone(u.cache.Read), id) {
		if retained[r] && !slices.Contains(keep, r) {
			keep = append(keep, r)
		}
	}
	slices.Sort(keep)
	u.cache.Read = keep
	if err := u.cache.save(u.cachePath); err != nil {
		u.flash, u.flashErr = "local busmon cache not saved", true
	}
}

// unreadEvents are the entries marked unread at the last fill, oldest first.
func (u *ui) unreadEvents() []bus.Event {
	var out []bus.Event
	for _, fl := range u.feed {
		if fl.mark == markUnread {
			out = append(out, fl.ev)
		}
	}
	return out
}

// nextUnread selects the next unread Activity line after the selection,
// wrapping; false when none.
func (u *ui) nextUnread() bool {
	l := u.lists[PanelActivity]
	cur := u.feedIndex(l.Selected())
	var first = -1
	for i, fl := range u.feed {
		if fl.mark != markUnread {
			continue
		}
		if first < 0 {
			first = i
		}
		if i > cur {
			l.Select(fl.id)
			return true
		}
	}
	if first >= 0 {
		l.Select(u.feed[first].id)
		return true
	}
	return false
}
