package agentstate

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Background work: herdr exit step 2b, spec 2026-11-10-herdr-exit-design.md
// 4.3b. An agent can end its turn while a subagent or a background command
// still runs; a /clear, relaunch or drain then loses work. A pure judgement,
// wired to nothing yet (step 9 makes the dispatcher skip on it).

// The sources of background facts a profile may declare authoritative.
const (
	SourceHooks   = "hooks"   // subagent start/stop pairs, agy's Stop field fullyIdle
	SourceProcess = "process" // the agent's descendants, seen by its host
	SourceScreen  = "screen"  // a running-tasks list the client shows
)

var sources = map[string]bool{SourceHooks: true, SourceProcess: true, SourceScreen: true}

// HelperCPU is how active a helper may be and still be idle: CPU time grown
// by at most this share of the window (agentstate.helper_cpu, 4.3b; a setting
// once the dispatcher reads it).
const HelperCPU = 0.02

// Background is a client's background-work profile. Authoritative: the
// sources whose "none" counts; a client whose tools need not run under its
// own process (Codex's app-server, Grok's leader) never lists process.
// Helpers: command names of the client's long-lived children (MCP servers,
// its daemons), idle unless active. Screen: a running-tasks list on screen.
type Background struct {
	Authoritative []string
	Helpers       []*regexp.Regexp
	Screen        []*regexp.Regexp
}

// Proc is one descendant of the agent's process. CPUGrowth: CPU time it used
// over the facts' window.
type Proc struct {
	Name      string
	Start     time.Time
	CPUGrowth time.Duration
}

// ProcessFacts are the agent's descendants over the last Window.
type ProcessFacts struct {
	Window time.Duration
	Procs  []Proc
}

// HookFacts are what the client's hooks said since its session started:
// subagents running, and agy's fullyIdle from its last Stop (nil: not said).
type HookFacts struct {
	Subagents int
	FullyIdle *bool
}

// Apply counts one hook event. A start adds, a stop removes (never below
// zero), and SessionStart resets everything, so a lost stop cannot hold the
// fact forever. The caller starts from zero when the process ends.
func (h HookFacts) Apply(event string) HookFacts {
	switch event {
	case "SubagentStart":
		h.Subagents++
	case "SubagentStop":
		if h.Subagents > 0 {
			h.Subagents--
		}
	case "SessionStart":
		h = HookFacts{}
	}
	return h
}

// Facts are what is known of an agent now; a nil source was not read.
// ExtraHelpers: the MCP servers the launcher configured for it, by command
// name.
type Facts struct {
	Process      *ProcessFacts
	Hooks        *HookFacts
	Screen       *string
	ExtraHelpers []string
}

// BackgroundState is the judgement. Its zero value is unknown: fail closed.
type BackgroundState int

const (
	BackgroundUnknown BackgroundState = iota
	BackgroundNone
	BackgroundRunning
)

func (s BackgroundState) String() string {
	return [...]string{"unknown", "none", "running"}[s]
}

// Judgement is the background fact of one agent, with what runs.
type Judgement struct {
	State   BackgroundState
	running []string
}

// What lists what runs: `subagent ×2, process 'pytest' 4m`.
func (j Judgement) What() string { return strings.Join(j.running, ", ") }

// Blocks: running or unknown, so /clear, relaunch, swap, suspend, pop-retire
// and drain skip the agent (4.3b); a wake is still allowed.
func (j Judgement) Blocks() bool { return j.State != BackgroundNone }

// Judge: running when any source says so, even one not authoritative; none
// only when every authoritative source was read and says none; unknown
// otherwise (no authoritative source, or one not read).
func (b Background) Judge(f Facts, now time.Time) Judgement {
	var j Judgement
	read := map[string]bool{}
	if h := f.Hooks; h != nil {
		read[SourceHooks] = true
		if h.Subagents > 0 {
			j.running = append(j.running, fmt.Sprintf("subagent ×%d", h.Subagents))
		}
		if h.FullyIdle != nil && !*h.FullyIdle {
			j.running = append(j.running, "background tasks")
		}
	}
	if p := f.Process; p != nil {
		read[SourceProcess] = true
		for _, pr := range p.Procs {
			if b.helper(pr.Name, f.ExtraHelpers) && float64(pr.CPUGrowth) <= HelperCPU*float64(p.Window) {
				continue
			}
			what := "process '" + clean(pr.Name, 40) + "'"
			if !pr.Start.IsZero() {
				what += " " + age(now.Sub(pr.Start))
			}
			j.running = append(j.running, what)
		}
	}
	if f.Screen != nil { // parseProfile refuses an authoritative screen with no pattern
		read[SourceScreen] = true
		if anyMatch(b.Screen, Strip(*f.Screen)) {
			j.running = append(j.running, "background work on screen")
		}
	}
	switch {
	case len(j.running) > 0:
		j.State = BackgroundRunning
	case len(b.Authoritative) == 0:
		j.State = BackgroundUnknown
	default:
		j.State = BackgroundNone
		for _, a := range b.Authoritative {
			if !read[a] {
				j.State = BackgroundUnknown
			}
		}
	}
	return j
}

func (b Background) helper(name string, extra []string) bool {
	for _, e := range extra {
		if name == e {
			return true
		}
	}
	return anyMatch(b.Helpers, name)
}

// age is a short duration: 30s, 4m, 2h.
func age(d time.Duration) string {
	switch {
	case d < 0: // a clock behind the start
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh", int(d/time.Hour))
}
