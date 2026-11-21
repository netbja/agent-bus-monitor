package main

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
)

// X on a service card (services-restart-buttons §3): the person restarts a
// service through the existing command, `agentbus --project <p> service
// restart <kind>`, asked first with that exact command, then run as the
// Projects page's x runs agentbus (startRun: one run at a time, the output in
// the project's log, its last line the notice). busmon asks nothing in an
// agent's environment or under an agent; the command itself keeps accepting
// every caller (the sentinel's repair). Holders stay read-only and Redis is
// setup's (the person's Q1, Q2).

const svcRestartVerb = "restart "

// askServiceRestart checks the selected card and asks, or says why not.
func (m *Model) askServiceRestart(p *page) tea.Cmd {
	refuse := func(why string) tea.Cmd {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	if why := agentcaller.Refuse(context.Background(), m.env, m.cfg.ancestor, "restarting a service", "agentbus --project "+m.cfg.project+" service restart"); why != "" {
		return refuse(why)
	}
	if p.sel >= len(svcCardKinds) {
		return refuse("Redis serves every project on this machine: setup restarts it, not busmon")
	}
	kind, f := svcCardKinds[p.sel], p.svc
	rec, known := f.recs[kind]
	if known && rec.Host != "" && rec.Host != f.host {
		return refuse(kind + " runs on " + rec.Host + ": restart it on " + rec.Host)
	}
	if f.inst == nil || f.inst.Host != f.host || !slices.Contains(f.inst.Kinds, kind) {
		return refuse("no " + kind + " service is installed here (agentbus up installs them)")
	}
	if r := m.ui.sleepRun; r != nil {
		return refuse(r.verb + " already running (see " + r.log + ")")
	}
	args := []string{"--project", m.cfg.project, "service", "restart", kind}
	m.ui.sleepAsk = &sleepAsk{verb: svcRestartVerb + kind, args: args}
	m.ui.flash, m.ui.flashErr = "Restart "+kind+" of "+m.cfg.project+"? agentbus "+strings.Join(args, " ")+". y / n", true
	return nil
}

// askServiceStart is S (project-switch-ux item 1): start a stopped
// project's services with agentbus service start (every installed one,
// dispatch then missions), after y. The agents stay down; the person's act,
// as X is.
func (m *Model) askServiceStart() tea.Cmd {
	refuse := func(why string) tea.Cmd {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	args := m.startServicesArgs()
	if why := agentcaller.Refuse(context.Background(), m.env, m.cfg.ancestor, "starting the services", "agentbus "+strings.Join(args, " ")); why != "" {
		return refuse(why)
	}
	switch d := m.snap.dispatch; {
	case d == "":
		return refuse("dispatch's state is not known yet: nothing started")
	case !stoppedStates[d]:
		return refuse("dispatch is " + d + ": the services run already (X on F3 restarts one)")
	}
	if r := m.ui.sleepRun; r != nil {
		return refuse(r.verb + " already running (see " + r.log + ")")
	}
	m.ui.sleepAsk = &sleepAsk{verb: "start services", args: args}
	m.ui.flash, m.ui.flashErr = "Start the services of "+m.cfg.project+" (dispatch, missions; the agents stay down)? agentbus "+strings.Join(args, " ")+". y / n", true
	return nil
}

// rereadServices reads the open Services page once after a restart, so its
// card shows the new process at once; the page's tick chain goes on as it was.
func (m *Model) rereadServices(verb string) tea.Cmd {
	if !strings.HasPrefix(verb, svcRestartVerb) {
		return nil
	}
	i := slices.IndexFunc(m.rd.pages, func(p page) bool { return p.kind == ovServices })
	if i < 0 || m.rd.pages[i].svcBusy {
		return nil
	}
	p := &m.rd.pages[i]
	p.svcBusy = true
	read := m.servicesCmd(p.serial)
	return func() tea.Msg {
		msg := read().(servicesMsg)
		msg.once = true
		return msg
	}
}
