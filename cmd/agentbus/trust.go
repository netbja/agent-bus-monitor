package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/trust"
)

// Workspace trust (specialist roles spec, part B). A client asks "do you
// trust this folder?" the first time it starts in a new project. agentbus up
// starts one agent per untrusted client first, lets the person answer that
// dialog in its pane, and starts the others once the trust is recorded. It
// only ever reads the clients' files, and only the trust flag: the readers
// return a bool, never content (package trust), so nothing of these files
// (which may hold account data or an API key) can reach a log or an error.

// errTrustPending: the person did not accept the trust in time; the agents
// already started stay, and agentbus up starts the others once it is.
var errTrustPending = errors.New("trust not accepted")

// trustPlan splits the template's tabs: those started at once (every tab
// but an agent of a client not yet trusted, and the first agent of each such
// client, whose pane shows the dialog) and those started once the trust is
// accepted. gates maps each untrusted client to its first agent's tab.
func trustPlan(tabs []upTab, clientOf func(string) string, trusted func(string) bool) (now, later []int, gates map[string]string) {
	gates = map[string]string{}
	known := map[string]bool{}
	for i, t := range tabs {
		if strings.TrimSpace(t.Command) == "" {
			continue
		}
		client := ""
		if clientOf != nil {
			client = clientOf(t.Command)
		}
		if !trust.Gated[client] {
			now = append(now, i)
			continue
		}
		ok, seen := known[client]
		if !seen {
			ok = trusted(client)
			known[client] = ok
		}
		switch _, first := gates[client]; {
		case ok:
			now = append(now, i)
		case !first:
			gates[client] = t.Name
			now = append(now, i)
		default:
			later = append(later, i)
		}
	}
	return now, later, gates
}

// waitTrust tells the person where to answer, polls the trust (reading only
// the flag) and starts the rest once every gated client is trusted.
func (d upDeps) waitTrust(tpl upTemplate, dir string, gates, paneOf map[string]string, start func() error) error {
	key, _ := trust.Key(dir)
	clients := slices.Sorted(maps.Keys(gates))
	for _, c := range clients {
		where := gates[c] + "'s pane"
		if p := paneOf[gates[c]]; p != "" {
			where += " (" + p + ")"
		}
		fmt.Fprintf(d.Out, "%s asks whether you trust %s: answer it in %s. The other agents start once it is accepted (agentbus up %s is locked meanwhile).\n",
			trust.Label(c), key, where, tpl.Name)
	}
	poll, timeout := d.TrustPoll, d.TrustTimeout
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	for waited := time.Duration(0); ; waited += poll {
		all := true
		for _, c := range clients {
			all = all && d.Trusted(c, dir)
		}
		if all {
			return start()
		}
		if waited >= timeout {
			return fmt.Errorf("%w for %s within %s: the other agents are not started; once you have answered, run agentbus up %s",
				errTrustPending, key, timeout, tpl.Name)
		}
		d.Sleep(poll)
	}
}

// trustAskBus is what the trust reminder needs of the project's asks.
type trustAskBus interface {
	Ask(ctx context.Context, def bus.AskDef, replaces string) (string, string, error)
	Asks(ctx context.Context) ([]bus.Ask, error)
	WithdrawQuestion(ctx context.Context, agent, askID, qid, note string) (int, error)
}

// trustAskFolder opens a reminder's context: the folder it is about, what
// binds the reminder to it (reviewer-codex).
func trustAskFolder(key string) string { return "Folder: " + key + ". " }

// trustAskHeader heads a trust reminder's question: with its id, what tells
// it from any other question.
const trustAskHeader = "Folder trust"

// trustAskQID is a trust reminder's question id: the client it is about.
func trustAskQID(client string) string { return "trust-" + client }

// trustAskDef is the reminder (first-run-trust (b), the person's answer):
// the folder, where to answer (the gate agent's pane, which shows the
// dialog), the choice by its label (the keys only as a hint: they change
// between client versions) and what trusting loads. Asked from agentbus
// (trustAskFrom). Nothing is written to a client's file.
func trustAskDef(client, key, where string) bus.AskDef {
	label := trust.Label(client)
	hint := ""
	if client == "claude-code" {
		hint = " In Claude Code 2.1: ↓ then Enter (a hint: the keys change between versions)."
	}
	title := "Trust " + filepath.Base(key) + "?" // the first client's folder: every reminder of one up is about the same folder
	if r := []rune(title); len(r) > 100 {
		title = string(r[:99]) + "…"
	}
	return bus.AskDef{From: trustAskFrom, Title: title, Questions: []bus.AskQuestion{{
		ID:       trustAskQID(client),
		Header:   trustAskHeader,
		Question: label + ` asks whether you trust this folder: in ` + where + `, choose "` + trust.Choice + `".`,
		Context: trustAskFolder(key) + "Trusting lets " + label + " read, edit and run files there, and load the repository's own " +
			"settings, hooks and MCP servers: for a repository you did not write, look at it first." + hint +
			" This ask closes itself once the trust is recorded.",
		Options: []bus.AskOption{{Label: "Done in the pane", Description: "it also closes by itself once the trust is recorded"}},
	}}}
}

// trustAskFrom is the trust reminders' sender: agentbus itself, never an
// agent. An ask takes its sender's one open-ask slot, and an agent's open ask
// also mutes its prompt-notify ring and holds its auto-clear: asked from the
// gate agent, the reminder would block that agent's own asks, a --replace
// would drop it, and the desktop would not ring for the very dialog it is
// about (foureyes, reviewer-codex).
const trustAskFrom = bus.SystemSender

// trustReminders settles and opens the trust reminders: one ask from
// agentbus, a question trust-<client> headed "Folder trust" per untrusted
// gated client, naming the pane to answer in. A reminder whose client now
// trusts dir is withdrawn; each untrusted gated client without one gets its
// question, added to agentbus's open ask through the bus's replace (its
// other questions kept). It returns what closes the reminders once the trust
// is accepted. A broker error is a note when there is a dialog to answer,
// silent otherwise (foureyes).
func (d upDeps) trustReminders(tpl upTemplate, dir string, gates, paneOf map[string]string) func() {
	noop := func() {}
	if d.TrustAsks == nil || d.Trusted == nil {
		return noop
	}
	note := func(err error) {
		if len(gates) > 0 {
			fmt.Fprintf(d.Out, "busmon ask not opened (%v): answer the dialog as said above\n", err)
		}
	}
	ab, err := d.TrustAsks(tpl.Name)
	if err != nil {
		note(err)
		return noop
	}
	ctx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 5*time.Second)
	}
	key, _ := trust.Key(dir)
	var mine *bus.Ask              // agentbus's open ask
	openFor := map[string]string{} // client -> the ask holding its open reminder for key
	read := func() (bool, error) { // true: stale reminders were withdrawn, read again
		c, cancel := ctx()
		asks, err := ab.Asks(c)
		cancel()
		if err != nil {
			return false, err
		}
		mine, openFor = nil, map[string]string{}
		var stale [][2]string // client, ask: a reminder about another folder (the template moved, reviewer-codex)
		for i, a := range asks {
			if a.State != "open" || a.From != trustAskFrom {
				continue
			}
			mine = &asks[i]
			for _, q := range a.Questions {
				client, ok := strings.CutPrefix(q.ID, "trust-")
				switch {
				case !ok || q.Answer != nil || q.Header != trustAskHeader:
				case strings.HasPrefix(q.Context, trustAskFolder(key)):
					openFor[client] = a.ID
				default:
					stale = append(stale, [2]string{client, a.ID})
				}
			}
		}
		for _, st := range stale {
			c, cancel := ctx()
			_, err := ab.WithdrawQuestion(c, trustAskFrom, st[1], trustAskQID(st[0]), "the project's folder is now "+key)
			cancel()
			if err != nil {
				return false, err
			}
		}
		return len(stale) > 0, nil
	}
	again, err := read()
	if again && err == nil {
		_, err = read()
	}
	if err != nil {
		note(err)
		return noop
	}
	closeTrusted := func() {
		for client, id := range openFor {
			if !d.Trusted(client, dir) {
				continue
			}
			c, cancel := ctx()
			if _, err := ab.WithdrawQuestion(c, trustAskFrom, id, trustAskQID(client), "trusted: recorded by "+trust.Label(client)); err == nil {
				delete(openFor, client)
			}
			cancel()
		}
	}
	closeTrusted()
	for _, client := range slices.Sorted(maps.Keys(gates)) {
		if _, ok := openFor[client]; ok {
			continue
		}
		agent := gates[client]
		where := agent + "'s pane"
		if p := paneOf[agent]; p != "" {
			where += " (" + p + ")"
		}
		def, replaces := trustAskDef(client, key, where), ""
		if mine != nil { // agentbus's open ask keeps its questions: this one joins them
			if len(mine.Questions) >= bus.AskMaxQuestions || slices.ContainsFunc(mine.Questions, func(q bus.AskQuestionState) bool { return q.ID == trustAskQID(client) && q.Answer == nil }) {
				note(fmt.Errorf("agentbus's open ask has no room for %s's reminder", trust.Label(client)))
				continue
			}
			merged := bus.AskDef{From: trustAskFrom, Title: mine.Title, Task: mine.Task}
			for _, q := range mine.Questions {
				if q.Answer == nil { // its open reminders only: a closed one would hold its id
					merged.Questions = append(merged.Questions, q.AskQuestion)
				}
			}
			def, replaces = bus.AskDef{From: trustAskFrom, Title: merged.Title, Task: merged.Task, Questions: append(merged.Questions, def.Questions...)}, mine.ID
		}
		c, cancel := ctx()
		id, _, err := ab.Ask(c, def, replaces)
		cancel()
		if err != nil {
			note(err)
			continue
		}
		for other, held := range openFor { // a replace moved the earlier reminders to the new ask
			if held == replaces {
				openFor[other] = id
			}
		}
		openFor[client] = id
		mine = &bus.Ask{ID: id, From: trustAskFrom, Title: def.Title, Task: def.Task, State: "open"}
		for _, q := range def.Questions {
			mine.Questions = append(mine.Questions, bus.AskQuestionState{AskQuestion: q})
		}
	}
	return closeTrusted
}

// tabClient is the client a template tab's command launches: the role after
// agent-launch, resolved in the manifest the command names (ROLES_TOML=...),
// else the caller's. "" when the tab launches no agent or the role does not
// resolve (no gate for it: today's behaviour).
func tabClient(command string, env func(string) string) string {
	p, ok, _ := tabProfile(command, env)
	if !ok {
		return ""
	}
	return p.Client
}

// tabExecutable is the program a template tab's agent runs (installer-no-
// client): the builtin client's binary or the adapter's executable, "" when
// the tab launches no agent or the role does not resolve.
func tabExecutable(command string, env func(string) string) string {
	p, ok, _ := tabProfile(command, env)
	if !ok {
		return ""
	}
	return rolecfg.ClientExecutable(p)
}

// tabProfile resolves the profile a tab's agent-launch runs: the role, on the
// --profile the tab names (launchProfile), in the manifest the command names,
// else the caller's. ok is false, with no error, when the tab launches no
// agent, the manifest cannot be loaded or the role is unknown (today's skip);
// a profile roles.toml does not allow for the role is an error (checkProfiles
// stops up on it).
func tabProfile(command string, env func(string) string) (rolecfg.Profile, bool, error) {
	manifest, role, profile := tabLaunch(command)
	if manifest == "" {
		manifest = env("ROLES_TOML")
	}
	if role == "" || manifest == "" {
		return rolecfg.Profile{}, false, nil
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		return rolecfg.Profile{}, false, nil
	}
	if _, known := m.Roles[role]; !known {
		return rolecfg.Profile{}, false, nil
	}
	p, err := launchProfile(m, role, profile)
	if err != nil {
		return rolecfg.Profile{}, false, err
	}
	return p, true, nil
}
