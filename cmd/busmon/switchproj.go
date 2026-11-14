package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The project switch (spec 2026-11-14-busmon-project-switch-design.md): o on
// the Projects page and /project <name> open another project of this broker
// in this busmon. busmon ends and re-executes itself on it (§2.2): the
// project is woven through the process, and a fresh one cannot show, or
// write, the wrong project's data.

// switchCheckedMsg is the target's keys, read in a command.
type switchCheckedMsg struct {
	target string
	ok     bool
	err    error
}

// projectNamesMsg is the broker's projects, read once for /project's
// completions.
type projectNamesMsg struct{ names []string }

// switchProject refuses, with the flash and nothing changed, the current
// project and an open draft (drafts are in memory only, never dropped
// silently); then it reads the target's keys and, when it has some, ends
// busmon with the target set (switchTo): main saves the cache and execs.
func (m *Model) switchProject(target string) tea.Cmd {
	switch {
	case !bus.ValidName(target):
		m.ui.flash, m.ui.flashErr = fmt.Sprintf("%q is not a project name", target), true
		return nil
	case target == m.cfg.project:
		m.ui.flash, m.ui.flashErr = "already on "+target, true
		return nil
	case m.switchDraft():
		m.ui.flash, m.ui.flashErr = "a draft is open: send it or clear it first", true
		return nil
	case m.cfg.projectHasKeys == nil:
		m.ui.flash, m.ui.flashErr = "no broker to check "+target+" on", true
		return nil
	}
	has, ctx := m.cfg.projectHasKeys, m.cfg.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ok, err := has(c, target)
		return switchCheckedMsg{target: target, ok: ok, err: err}
	}
}

// switchDraft: a draft stands (a typed line, the compose window's text or
// held drafts, an answer being typed).
func (m *Model) switchDraft() bool {
	if strings.TrimSpace(m.ui.edit) != "" && !m.paletteOn() || strings.TrimSpace(m.ui.msgDraft) != "" || m.composeHasDraft() {
		return true
	}
	for _, d := range m.ui.askDrafts {
		if strings.TrimSpace(d) != "" {
			return true
		}
	}
	return false
}

// gotSwitchChecked ends busmon on a target with keys.
func (m *Model) gotSwitchChecked(msg switchCheckedMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.ui.flash, m.ui.flashErr = "cannot read "+msg.target+" on this broker: "+msg.err.Error(), true
		return nil
	case !msg.ok:
		m.ui.flash, m.ui.flashErr = msg.target+" is not on this broker: agentbus up "+msg.target+" first", true
		return nil
	}
	m.switchTo = msg.target
	return tea.Quit
}

// palProject is /project <name>.
func (m *Model) palProject(args []string) tea.Cmd { return m.switchProject(args[0]) }

// projectNamesCmd reads the broker's projects once (the palette's /project
// completions).
func (m *Model) projectNamesCmd() tea.Cmd {
	list, ctx := m.cfg.listProjects, m.cfg.ctx
	if list == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, _ := list(c)
		return projectNamesMsg{names: names}
	}
}

// cacheSnapshot is the cache as saveCache writes it.
func (m *Model) cacheSnapshot() busmonCache {
	c := *m.cache_()
	c.Snoozes = make(map[string]int64, len(m.sig.cache.Snoozes))
	for k, v := range m.sig.cache.Snoozes {
		c.Snoozes[k] = v
	}
	c.Read = append([]string(nil), m.sig.cache.Read...)
	return c
}

// execFn is how main re-executes busmon (a test replaces it).
var execFn = func(path string, argv, env []string) error { return syscallExec(path, argv, env) }

// finishSwitch is main's end of a switch, after the program quit and the
// terminal was restored: the old project's cache written synchronously (the
// model's saveCache is an asynchronous command that tea.Quit may outrun),
// then busmon re-executed on the target. A cache that cannot be written, or
// an exec that fails, stops with the command to type.
func finishSwitch(fm Model, opts busmonOpts, set map[string]bool, stderr io.Writer) int {
	argv := switchArgv(opts, set, fm.switchTo)
	again := "busmon " + strings.Join(argv, " ")
	if path := fm.cfg.cachePath; path != "" {
		c := fm.cacheSnapshot()
		if err := c.save(path); err != nil {
			fmt.Fprintf(stderr, "error: %s's read marks not saved: %v\nopen %s with: %s\n", fm.cfg.project, err, fm.switchTo, again)
			return 1
		}
	}
	exe, err := os.Executable()
	if err == nil {
		err = execFn(exe, append([]string{exe}, argv...), os.Environ())
	}
	fmt.Fprintf(stderr, "error: cannot open %s: %v\nopen it with: %s\n", fm.switchTo, err, again)
	return 1
}
