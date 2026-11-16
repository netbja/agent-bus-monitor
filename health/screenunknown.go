package health

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The screen-unknown check (vt-osc-title, 2026-11-16): the dispatcher wakes
// an idle agent only when it can read the agent's screen (ready). An agent
// declared idle whose screen has read unknown this long cannot be woken: on
// 2026-11-16 every Claude agent on a holder was in that state after its
// first turn, and nothing said so. The check reads the dispatcher's
// observations ({p}:observed); it never judges a screen itself.

// ScreenUnknownAfter is how long an idle agent's screen may read unknown.
const ScreenUnknownAfter = 10 * time.Minute

// ScreenEvidence is one agent: its declared state, the dispatcher's last
// observation of its screen (Fresh: not aged out) and whether its client has
// a signature to read a screen with (Judged).
type ScreenEvidence struct {
	Agent, Declared, Observed string
	Since                     int64 // Unix ms: the observed state's start
	Fresh, Judged             bool
}

// EvaluateScreenUnknown: a warn naming each idle agent whose screen has read
// unknown for ScreenUnknownAfter or more; ok otherwise; unknown when no
// fresh observation of an agent whose screen can be read exists.
func EvaluateScreenUnknown(ev []ScreenEvidence, nowMs int64) Outcome {
	seen := 0
	var stuck []string
	sort.Slice(ev, func(i, j int) bool { return ev[i].Agent < ev[j].Agent })
	for _, e := range ev {
		if !e.Fresh || !e.Judged {
			continue
		}
		seen++
		if e.Declared != "idle" || e.Observed != "unknown" || e.Since <= 0 {
			continue
		}
		if d := time.Duration(nowMs-e.Since) * time.Millisecond; d >= ScreenUnknownAfter {
			stuck = append(stuck, fmt.Sprintf("%s %d min", e.Agent, int64(d/time.Minute)))
		}
	}
	switch {
	case seen == 0:
		return Outcome{Status: Unknown, Detail: "no screen observed (the dispatch service reads them)"}
	case len(stuck) == 0:
		return Outcome{Status: OK, Detail: fmt.Sprintf("%d screen(s) read", seen)}
	}
	return Outcome{Status: Warn, Detail: truncateDetail("idle, screen unreadable, cannot be woken: " + strings.Join(stuck, ", "))}
}
