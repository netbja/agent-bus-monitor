package health

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
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
	Live     []string      // its live terminals' ids, when it answers
}

// HolderAgent is an agent whose last proved identity is a holder terminal
// (its pane "<build key>/<terminal>"), with that proof's age on Redis's clock.
type HolderAgent struct {
	Name, Key, Term string
	ProvedAgo       time.Duration
}

// HolderProof is an agent's last proved identity as {p}:agents records it:
// its pane, its verified pid (0: unproved) and when (ms, Redis time).
type HolderProof struct {
	Name, Pane string
	PID        int
	IdentTS    int64
}

// WithHolderAgents adds to e the agents proved in a holder terminal (their
// pane "<build key>/<terminal>"), each with its proof's age on Redis's
// clock (redisNow), for holder_restarted; any other agent is left out.
func WithHolderAgents(e HolderEvidence, proofs []HolderProof, redisNow time.Time) HolderEvidence {
	e.AgentsKnown = true
	sorted := slices.Clone(proofs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, p := range sorted {
		key, term, ok := HolderPane(p.Pane)
		if !ok || p.PID <= 0 || p.IdentTS <= 0 {
			continue
		}
		e.Agents = append(e.Agents, HolderAgent{Name: p.Name, Key: key, Term: term, ProvedAgo: redisNow.Sub(time.UnixMilli(p.IdentTS))})
	}
	return e
}

// HolderPane splits an agent's pane into a holder's build key and terminal
// id; ok is false for a herdr pane or anything else.
func HolderPane(pane string) (key, term string, ok bool) {
	key, term, found := strings.Cut(pane, "/")
	if !found || !service.ValidBuildKey(key) || !holderTermID.MatchString(term) {
		return "", "", false
	}
	return key, term, true
}

var holderTermID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// HolderEvidence is every build found: units, sockets and binary copies.
type HolderEvidence struct {
	Known  bool
	Builds []HolderBuild
	// The agents proved in a holder terminal (holder_restarted); unread
	// when AgentsKnown is false.
	AgentsKnown bool
	Agents      []HolderAgent
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
		if w := holderRestarted(e); w != "" {
			return Outcome{Status: Warn, Detail: w}
		}
		return Outcome{Status: OK, Detail: fmt.Sprintf("holder build %s answers, %d terminal(s)", cur.Raw, cur.Terms)}
	}
	for _, b := range e.Builds {
		if b.Answers {
			return Outcome{Status: Fail, Detail: fmt.Sprintf("the current build %s's holder does not answer (build %s still does)", cur.Raw, b.Raw)}
		}
	}
	return Outcome{Status: Fail, Detail: fmt.Sprintf("the holder service of build %s is installed and no holder answers", cur.Raw)}
}

// holderRestarted is the holder_restarted warning (spec 4.4a), "" when
// none: a holder that answers started after an agent's last proof in one of
// its terminals, and that terminal is gone, so the agent died with the
// holder's old process. The socket's age (this host's clock) and the proof's
// (Redis's) are each measured on their own clock.
func holderRestarted(e HolderEvidence) string {
	if !e.AgentsKnown {
		return ""
	}
	var parts []string
	for _, b := range e.Builds {
		if !b.Answers {
			continue
		}
		var lost []string
		for _, a := range e.Agents {
			if a.Key == b.Key && a.ProvedAgo > b.Since && !slices.Contains(b.Live, a.Term) {
				lost = append(lost, a.Name)
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			parts = append(parts, fmt.Sprintf("holder build %s restarted %s ago: %d agent(s) lost: %s", b.Raw, b.Since.Round(time.Second), len(lost), strings.Join(lost, ", ")))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + " (agentbus up or self-repair brings them back; nothing is relaunched by itself)"
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
