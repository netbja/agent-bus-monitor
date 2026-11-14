package health

import (
	"fmt"
	"strings"
	"time"
)

// The holder's checks (herdr exit spec 4.4, 4.4a). Each build of the
// project's holder, as the service manager and its socket report it.
type HolderBuild struct {
	Key, Raw string
	Current  bool          // the build of the agentbus that measures
	Unit     bool          // its unit (or launchd job) is installed
	Active   bool          // the manager runs it, or is starting it
	Answers  bool          // its socket answers
	Terms    int           // its terminals, when it answers
	Since    time.Duration // how long its socket has existed, when it answers
	Binary   bool          // its binary copy is on disk
}

// HolderEvidence is every build found: units, sockets and binary copies.
type HolderEvidence struct {
	Known  bool
	Builds []HolderBuild
}

func (e HolderEvidence) current() (HolderBuild, bool) {
	for _, b := range e.Builds {
		if b.Current {
			return b, true
		}
	}
	return HolderBuild{}, false
}

// EvaluateHolder: fail when the current build's unit is installed and no
// holder of the project answers (every agent it held is gone; sentinel may
// start it once). No unit for the current build: nothing to measure.
func EvaluateHolder(e HolderEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "holder units or sockets unavailable"}
	}
	cur, ok := e.current()
	if !ok || !cur.Unit {
		return Outcome{Status: OK, Detail: "no holder service for this build"}
	}
	if cur.Answers {
		return Outcome{Status: OK, Detail: fmt.Sprintf("holder build %s answers, %d terminal(s)", cur.Raw, cur.Terms)}
	}
	for _, b := range e.Builds {
		if b.Answers {
			return Outcome{Status: Fail, Detail: fmt.Sprintf("the current build %s's holder does not answer (build %s still does)", cur.Raw, b.Raw)}
		}
	}
	return Outcome{Status: Fail, Detail: fmt.Sprintf("the holder service of build %s is installed and no holder answers", cur.Raw)}
}

// EvaluateHolderDraining: warn while an older build still holds terminals
// (an upgrade drains, never restarts; spec 4.4). One that answers with none
// is about to exit.
func EvaluateHolderDraining(e HolderEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "holder sockets unavailable"}
	}
	var old []string
	for _, b := range e.Builds {
		if !b.Current && b.Answers && b.Terms > 0 {
			old = append(old, fmt.Sprintf("build %s: %d terminal(s), for %s", b.Raw, b.Terms, b.Since.Truncate(time.Minute)))
		}
	}
	if len(old) == 0 {
		return Outcome{Status: OK, Detail: "no older holder build holds a terminal"}
	}
	return Outcome{Status: Warn, Detail: "an older holder still holds agents: " + strings.Join(old, "; ")}
}

// EvaluateHolderLeftovers: warn when an older build that neither runs nor
// answers still has its unit or binary copy: a gc did not remove it.
func EvaluateHolderLeftovers(e HolderEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "holder units or binary copies unavailable"}
	}
	var left []string
	for _, b := range e.Builds {
		if b.Current || b.Active || b.Answers || (!b.Unit && !b.Binary) {
			continue
		}
		var what []string
		if b.Unit {
			what = append(what, "unit")
		}
		if b.Binary {
			what = append(what, "binary")
		}
		left = append(left, "build "+b.Raw+" ("+strings.Join(what, ", ")+")")
	}
	if len(left) == 0 {
		return Outcome{Status: OK, Detail: "no older holder build left"}
	}
	return Outcome{Status: Warn, Detail: "older holder builds left after the gc: " + strings.Join(left, "; ")}
}
