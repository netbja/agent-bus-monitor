package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Class C (palette spec §4.1, slice 2): the person's agentbus commands. A
// row builds its argv from checked arguments (§4.4); busmon names the
// project once, as the global flag before the command (the form Z uses),
// shows the whole argv and runs it after y, on Z's run (startRun: one at a
// time, no shell, the output in the project's suspend.log, the outcome a
// notice). --as <identity.as> goes only to the three commands that take it;
// with no identity.as they are refused before y, never a default name.

// classCRows are the class C rows, in the table's order after the others.
func classCRows() []palCmd {
	name := func(what string) func(string) error {
		return func(s string) error {
			if !bus.ValidName(s) {
				return fmt.Errorf("%q is not a%s name", s, what)
			}
			return nil
		}
	}
	agents := func(m *Model) []string { return m.names(false) }
	role := palArg{Name: "role", Check: name(" role"), Complete: agents}
	agent := palArg{Name: "agent", Check: name("n agent"), Complete: agents}
	fresh := palArg{Name: "fresh", Literal: "fresh", Optional: true, Complete: func(*Model) []string { return []string{"fresh"} }}
	reset := palArg{Name: "reset", Literal: "reset", Complete: func(*Model) []string { return []string{"reset"} }}
	dur := palArg{Name: "duration", Check: func(s string) error {
		// the parser pause --for uses (time.ParseDuration), and a positive one
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			return fmt.Errorf("%q is not a duration (2h, 90m)", s)
		}
		return nil
	}}
	task := palArg{Name: "task", Check: func(s string) error {
		if !bus.ValidName(s) { // SetRequestPriority's own check, here before y (reviewer-codex)
			return fmt.Errorf("%q is not a task name", s)
		}
		return nil
	}, Complete: func(m *Model) []string {
		var out []string
		for _, r := range m.snap.requests {
			out = append(out, r.Task)
		}
		return out
	}}
	level := palArg{Name: "level", Check: func(s string) error {
		if !bus.ValidPriority(s) {
			return fmt.Errorf("%q is not low, normal or high", s)
		}
		return nil
	}, Complete: func(*Model) []string { return []string{bus.PriorityHigh, bus.PriorityNormal, bus.PriorityLow} }}
	return []palCmd{
		{Name: "reload", Summary: "restart an idle agent on its role, resuming its session (fresh: without)", Args: []palArg{role, fresh}, As: true,
			Argv: func(a []string) []string {
				if len(a) == 2 {
					return []string{"reload", a[0], "--fresh"}
				}
				return []string{"reload", a[0]}
			}},
		{Name: "pause", Summary: "nobody is woken for that long", Args: []palArg{dur},
			Argv: func(a []string) []string { return []string{"pause", "--for", a[0]} }},
		{Name: "resume", Summary: "end the pause; a suspended project comes back (as Z)",
			Argv: func([]string) []string { return []string{"resume", "--yes"} }},
		{Name: "suspend", Summary: "put the project to sleep (as Z)",
			Argv: func([]string) []string { return []string{"suspend", "--yes"} }},
		{Name: "backup", Summary: "one archive of the project",
			Argv: func([]string) []string { return []string{"backup"} }},
		{Name: "self-repair", Summary: "clear an agent's crash loop and restart its budget", Args: []palArg{reset, agent}, As: true,
			Argv: func(a []string) []string { return []string{"self-repair", "reset", a[1]} }},
		{Name: "priority", Aliases: []string{"prio"}, Summary: "a request's priority: low, normal or high", Args: []palArg{task, level}, As: true,
			Argv: func(a []string) []string { return []string{"request", "priority", a[0], a[1]} }},
	}
}

// palAgentbus asks for y with the exact argv; y runs it (sleepConfirm).
// A refusal (no identity.as, a run already going) keeps the line.
func (m *Model) palAgentbus(c *palCmd, args []string) tea.Cmd {
	if r := m.ui.sleepRun; r != nil {
		m.ui.flash, m.ui.flashErr = r.verb+" already running (see "+r.log+")", true
		return nil
	}
	argv := append([]string{"--project", m.cfg.project}, c.Argv(args)...)
	if c.As {
		as := m.cfg.settings.Identity
		if as == "" {
			m.ui.flash, m.ui.flashErr = c.Name+" needs your name: set identity.as (/set identity.as)", true
			return nil
		}
		argv = append(argv, "--as", as)
	}
	m.leaveLine(false)
	m.closeReader()
	m.ui.sleepAsk = &sleepAsk{verb: c.Name, args: argv}
	m.ui.flash, m.ui.flashErr = "Run agentbus "+strings.Join(argv, " ")+"? y / n", true
	return nil
}
