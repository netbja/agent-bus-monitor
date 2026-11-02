package health

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// pop-done (spec docs/superpowers/specs/2026-11-03-pop-auto-retire-design.md
// §7): a pop the dispatcher told master to end is still there long after.
// The directive's mark names the episode, not when it was sent, so "done for
// twice the delay" is read from the declaration: idle three times
// pop_retire_after (the delay, then twice it with master told). Never fail:
// it wakes nobody, master already has the directive.

// PopDoneAgent is one registered pop: since its idle declaration, and
// whether the dispatcher's mark names that episode (master was told).
type PopDoneAgent struct {
	Name  string
	Since time.Time
	Told  bool
}

// PopDoneInput is what EvaluatePopDone judges.
type PopDoneInput struct {
	Off   bool // dispatch.pop_retire off or its delay 0
	Now   time.Time
	After time.Duration
	Pops  []PopDoneAgent
}

// EvaluatePopDone warns on every pop told and still registered past the delay.
func EvaluatePopDone(in PopDoneInput) Outcome {
	if in.Off {
		return Outcome{Status: Unknown, Detail: "off (dispatch.pop_retire)"}
	}
	var late []string
	for _, p := range in.Pops {
		if age := in.Now.Sub(p.Since); p.Told && age >= 3*in.After {
			late = append(late, fmt.Sprintf("%s done %s ago, not ended", p.Name, strings.TrimSuffix(age.Round(time.Minute).String(), "0s")))
		}
	}
	if len(late) == 0 {
		return Outcome{Status: OK}
	}
	sort.Strings(late)
	return Outcome{Status: Warn, Detail: strings.Join(late, "; ") + " (master was told: agentbus retire <role> --forget)"}
}
