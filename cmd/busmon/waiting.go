package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// waitingAnswerAt finds the first retained, explicitly human message addressed
// to agent after its declaration, or a human reply in a retained thread rooted
// by that agent. Stream IDs are millisecond timestamps; same-ms events are not
// ordered across streams and therefore do not count as later.
func waitingAnswerAt(agent string, declaredAt time.Time, events []bus.Event) time.Time {
	if agent == "" || declaredAt.IsZero() {
		return time.Time{}
	}
	roots := make(map[string]bool)
	for _, e := range events {
		if e.Kind == "cmd" && e.From == agent {
			if id := threadID(e); id != "" {
				roots[id] = true
			}
		}
	}
	var first time.Time
	for _, e := range events {
		if e.Kind != "cmd" || !e.Human {
			continue
		}
		at := eventTimestamp(e.ID)
		if at.IsZero() || !at.After(declaredAt) {
			continue
		}
		if e.Target != agent && (e.Type != bus.CmdReply || e.Ref == "" || !roots[e.Ref]) {
			continue
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	return first
}

func eventTimestamp(id string) time.Time {
	ms, _, _ := strings.Cut(id, "-")
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

func attentionHistoryFloor(events []bus.Event) time.Time {
	var floor time.Time
	for _, e := range events {
		if at := eventTimestamp(e.ID); !at.IsZero() && (floor.IsZero() || at.Before(floor)) {
			floor = at
		}
	}
	return floor
}

// answered: a declared wait that a person has replied to since it was
// declared, with no open question left. A reply computed for an older
// declaration never covers a newer one, even before the next history scan.
func answered(a *agentState) bool {
	return a.state == "waiting" && a.openAsk == 0 && !a.answeredAt.IsZero() && a.answeredAt.After(a.stateAt)
}
