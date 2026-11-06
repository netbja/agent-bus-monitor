package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// waitingAnswerAt finds the first retained, explicitly human message addressed
// to agent after declaredAt, or a human reply in a retained thread rooted by
// that agent (bus.WaitAnswered's message proof). Same-ms events are not
// ordered across streams and therefore do not count as later.
func waitingAnswerAt(agent string, declaredAt time.Time, events []bus.Event) time.Time {
	if declaredAt.IsZero() {
		return time.Time{}
	}
	if ms := bus.WaitAnswered(agent, declaredAt.UnixMilli(), 0, nil, events); ms > 0 {
		return time.UnixMilli(ms)
	}
	return time.Time{}
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

// agentAnsweredAt is when the person answered a's waiting, through the rule
// the stall guards and the notifications share (bus.WaitAnswered,
// stale-waiting §6): a human message after it, or one of its asks answered.
func agentAnsweredAt(name string, a *agentState, asks []bus.Ask, events []bus.Event) time.Time {
	if a.stateAt.IsZero() {
		return time.Time{}
	}
	if ms := bus.WaitAnswered(name, a.stateAt.UnixMilli(), a.openAsk, asks, events); ms > 0 {
		return time.UnixMilli(ms)
	}
	return time.Time{}
}
