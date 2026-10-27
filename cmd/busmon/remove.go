package main

import (
	"time"
)

// liveReason says why an agent must not be forgotten from busmon, "" if it may.
func liveReason(a *agentState, now time.Time) string {
	switch {
	case a.armed:
		return "is listening on the bus"
	case !a.stateAt.IsZero() && now.Sub(a.stateAt) < staleAfter:
		return "declared a status " + humanAge(now.Sub(a.stateAt))
	}
	return ""
}
