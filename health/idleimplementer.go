package health

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

// idle-implementer (spec docs/superpowers/specs/2026-11-08-idle-implementer-design.md,
// the person's answers 2026-11-08: 15 minutes, a role with the implement
// skill, requests only): an implementer passed over while a request waits for
// it, a hole team-stall and review-queue cannot see (they need the whole team
// idle). Pure; the reader is stallReader.idleImplementer.
//
// The idle clock is the agent's {p}:agents state time (bus.EffectiveState),
// which a turn's end restarts where turn hooks run: an implementer that takes
// a turn, even a short one answering a wake, is not passed over for the next
// idle_after (foureyes; intended). One episode is an implementer and its
// requested set: idle, then working, then idle again on the same set never
// wakes master twice, as review-queue's episodes.

// IdleImplementerInput is everything idle-implementer judges from.
type IdleImplementerInput struct {
	Now          time.Time
	After        time.Duration // dispatch.idle_after; 0 = off
	ReadErr      string
	Agents       []StallAgent
	Implementers []string            // the roles with the implement skill
	Held         map[string]bool     // agent -> held by its provider's quota
	Requested    map[string][]string // target -> its requests in state requested (read or not, never accepted)
}

// requestsShown is how many requested tasks the detail names.
const requestsShown = 3

// passedOver is one implementer the check warns for.
type passedOver struct {
	name  string
	idle  time.Duration
	tasks []string // sorted
}

// EvaluateIdleImplementer: an implementer idle for After, its pane not busy,
// not held, with a request addressed to it in state requested, is a warn in
// hand by master; anything else is OK with its reason.
func EvaluateIdleImplementer(in IdleImplementerInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (idle_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	case len(in.Implementers) == 0:
		return Outcome{Status: OK, Detail: "no role has the implement skill"}
	}
	over, reason := passedOverOf(in)
	if len(over) == 0 {
		return Outcome{Status: OK, Detail: truncateDetail(reason)}
	}
	parts := make([]string, len(over))
	for i, o := range over {
		parts[i] = fmt.Sprintf("%s idle %s; requested: %s", o.name, minutes(o.idle), taskList(o.tasks))
	}
	return Outcome{Status: Warn, Detail: truncateDetail(strings.Join(parts, "; "))}
}

// passedOverOf are the implementers passed over, by name; reason is the
// first implementer's why not, when none is.
func passedOverOf(in IdleImplementerInput) ([]passedOver, string) {
	var over []passedOver
	reason := ""
	why := func(s string) {
		if reason == "" {
			reason = s
		}
	}
	agents := slices.Clone(in.Agents)
	slices.SortFunc(agents, func(a, b StallAgent) int { return strings.Compare(a.Name, b.Name) })
	for _, a := range agents {
		if !slices.Contains(in.Implementers, a.Name) || a.Dormant {
			continue
		}
		idle := in.Now.Sub(a.DeclaredAt)
		tasks := slices.Sorted(slices.Values(in.Requested[a.Name]))
		switch {
		case a.State != "idle":
			why(a.Name + " " + a.State)
		case idle < in.After:
			why(fmt.Sprintf("%s idle only %s", a.Name, minutes(idle)))
		case a.Pane == PaneBusy:
			why(a.Name + " busy (not declared)")
		case in.Held[a.Name]:
			why(a.Name + " held by its quota")
		case len(tasks) == 0:
			why("nothing requested for " + a.Name)
		default:
			over = append(over, passedOver{a.Name, idle, tasks})
		}
	}
	if reason == "" {
		reason = "no implementer registered"
	}
	return over, reason
}

// taskList names at most requestsShown tasks, then +N.
func taskList(tasks []string) string {
	if len(tasks) <= requestsShown {
		return strings.Join(tasks, ", ")
	}
	return fmt.Sprintf("%s +%d", strings.Join(tasks[:requestsShown], ", "), len(tasks)-requestsShown)
}

// IdleImplementerWakes: while idle-implementer warns, one waking directive to
// master per implementer passed over, once per episode (the implementer and
// its sorted requested ids).
func IdleImplementerWakes(in IdleImplementerInput, master string) []StallWake {
	if EvaluateIdleImplementer(in).Status != Warn {
		return nil
	}
	over, _ := passedOverOf(in)
	out := make([]StallWake, 0, len(over))
	for _, o := range over {
		sum := sha256.Sum256([]byte(o.name + "\n" + strings.Join(o.tasks, "\n")))
		out = append(out, StallWake{Mark: "stall-implementer:" + o.name, Episode: hex.EncodeToString(sum[:8]), Target: master,
			Text: fmt.Sprintf("Stall guard: %s idle %s while requested for it: %s. Dispatch the next independent item (no stacked PRs: one PR per step from origin/main), or tell %s why it waits.",
				o.name, minutes(o.idle), taskList(o.tasks), o.name)})
	}
	return out
}
