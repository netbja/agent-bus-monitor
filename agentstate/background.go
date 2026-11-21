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
	// SourceDeclared is the agent's own declared record (agentbus background):
	// always running while it lasts, never authoritative for none
	// (background-work-visible §4b).
	SourceDeclared = "declared"
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
	// DirectHelpers: the client's own direct children that are not a shell
	// are its helpers (its MCP stdio servers, its daemons), and so are their
	// descendants (npx's node, codegraph's watcher), never background work
	// whatever their CPU: the client runs every tool command under a
	// shell, so work it starts is a shell's descendant (Claude Code:
	// "bash -c source …snapshot…"). A client whose commands may be exec'd in
	// place by a one-command shell does not declare it.
	DirectHelpers bool
	// HelperBranches: the names of direct children that are the client's
	// helpers, with their subtrees, whatever their CPU (Codex's code-mode
	// host): for a client that cannot declare DirectHelpers, since only a
	// listed name is a helper and a command exec'd in place still counts.
	HelperBranches []*regexp.Regexp
}

// shells are the names a client's tool command runs under: a direct child by
// one of these names is a command, not a helper.
var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "ksh": true}

// Proc is one descendant of the agent's process. CPUGrowth: CPU time it used
// over the facts' window.
type Proc struct {
	Name      string
	Start     time.Time
	CPUGrowth time.Duration
	// Branch: the name of the agent's direct child whose subtree holds it,
	// its own name when it is that child; "" when unknown (DirectHelpers
	// reads it: a process under a helper is the helper's).
	Branch string
	// Seen is when the host first saw it (background-work-visible step 2):
	// OldestAge counts from it when Start is unknown. It never enters What(),
	// whose text the dispatcher's once-per-change records compare (foureyes).
	Seen time.Time
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
	Tasks     int // the other tasks in flight (shell, monitor…), from Claude's Stop list (background-work-visible step 1)
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
	Declared     *string // the agent's declared text, unexpired (nil: none)
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
	procs   int           // the process source's active non-helper descendants
	oldest  time.Duration // the oldest of them's age (0: none, or no start known)
	source  string        // the first source that said running
	base    *Judgement    // the judgement without the declaration (set only when it counted)
}

// Source is the source that made the judgement block: hooks, process,
// screen or declared; "unknown" when no authoritative source was read
// (background-work-visible §4a); "" when it does not block.
func (j Judgement) Source() string {
	switch {
	case j.State == BackgroundUnknown:
		return "unknown"
	case j.State == BackgroundRunning:
		return j.source
	}
	return ""
}

// WithoutDeclared is the judgement of the other sources alone: what the
// auto-clear reads once the agent's context episode has escalated (§4b).
func (j Judgement) WithoutDeclared() Judgement {
	if j.base != nil {
		return *j.base
	}
	return j
}

// Procs is how many active non-helper descendants the process source saw
// (background-work-visible step 2: the observation's bg_procs).
func (j Judgement) Procs() int { return j.procs }

// OldestAge is the oldest of them's age (bg_oldest), 0 when none.
func (j Judgement) OldestAge() time.Duration { return j.oldest }

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
	say := func(src, what string) {
		if len(j.running) == 0 {
			j.source = src
		}
		j.running = append(j.running, what)
	}
	if h := f.Hooks; h != nil {
		read[SourceHooks] = true
		if h.Subagents > 0 {
			say(SourceHooks, fmt.Sprintf("subagent ×%d", h.Subagents))
		}
		if h.Tasks > 0 {
			say(SourceHooks, fmt.Sprintf("background task ×%d", h.Tasks))
		}
		if h.FullyIdle != nil && !*h.FullyIdle {
			say(SourceHooks, "background tasks")
		}
	}
	if p := f.Process; p != nil {
		read[SourceProcess] = true
		for _, pr := range p.Procs {
			if b.helperBranch(pr.Branch) { // the client's own MCP server or daemon, or its child
				continue
			}
			if b.helper(pr.Name, f.ExtraHelpers) && float64(pr.CPUGrowth) <= HelperCPU*float64(p.Window) {
				continue
			}
			what := "process '" + clean(pr.Name, 40) + "'"
			j.procs++
			if !pr.Start.IsZero() {
				what += " " + age(now.Sub(pr.Start))
				j.oldest = max(j.oldest, now.Sub(pr.Start))
			} else if !pr.Seen.IsZero() {
				j.oldest = max(j.oldest, now.Sub(pr.Seen))
			}
			say(SourceProcess, what)
		}
	}
	if f.Screen != nil { // parseProfile refuses an authoritative screen with no pattern
		read[SourceScreen] = true
		if anyMatch(b.Screen, Strip(*f.Screen)) {
			say(SourceScreen, "background work on screen")
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
	if d := f.Declared; d != nil { // the agent's own words: running while they last (§4b)
		base := j
		say(SourceDeclared, "declared: "+clean(*d, 60))
		j.State, j.base = BackgroundRunning, &base
	}
	return j
}

// helperBranch: a process under one of the client's own direct children,
// any non-shell one with DirectHelpers, a listed one with HelperBranches.
func (b Background) helperBranch(branch string) bool {
	if branch == "" {
		return false
	}
	return b.DirectHelpers && !shells[branch] || anyMatch(b.HelperBranches, branch)
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
