package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Workspace trust (specialist roles spec, part B). A client asks "do you
// trust this folder?" the first time it starts in a new project. agentbus up
// starts one agent per untrusted client first, lets the person answer that
// dialog in its pane, and starts the others once the trust is recorded. It
// only ever reads the clients' files, and only the trust flag: the readers
// return a bool, never content, so nothing of these files (which may hold
// account data or an API key) can reach a log or an error.

// trustKey is where Claude Code keys a folder's trust: the git repository's
// root (for a worktree, its main checkout's root), else the folder itself.
func trustKey(dir string) (key string, inRepo bool) {
	dir = filepath.Clean(dir)
	for d := dir; ; d = filepath.Dir(d) {
		st, err := os.Lstat(filepath.Join(d, ".git"))
		switch {
		case err == nil && st.IsDir():
			return d, true
		case err == nil && st.Mode().IsRegular():
			b, err := os.ReadFile(filepath.Join(d, ".git"))
			gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
			if gitdir != "" && !filepath.IsAbs(gitdir) { // git worktree add --relative-paths
				gitdir = filepath.Join(d, gitdir)
			}
			if main, _, ok := strings.Cut(filepath.ToSlash(gitdir), "/.git/worktrees/"); err == nil && ok {
				return filepath.FromSlash(main), true
			}
			return d, true
		}
		if parent := filepath.Dir(d); parent == d {
			return dir, false
		}
	}
}

// claudeTrusted reads only projects[<key>].hasTrustDialogAccepted from
// ~/.claude.json. Outside a repository a trusted ancestor folder covers the
// folder, as Claude Code documents; a repository is covered only by its own
// root's entry. Any error (missing, unreadable, half-written while Claude
// rewrites it) is "not trusted".
func claudeTrusted(path, dir string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	defer clear(b)
	var f struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &f) != nil {
		return false
	}
	key, inRepo := trustKey(dir)
	if f.Projects[key].Accepted {
		return true
	}
	if inRepo {
		return false
	}
	for d := filepath.Dir(key); d != filepath.Dir(d); d = filepath.Dir(d) {
		if f.Projects[d].Accepted {
			return true
		}
	}
	return false
}

// codexTrusted reads only [projects."<path>"].trust_level from Codex's
// config.toml: trusted when the repository's root, or the folder itself, is
// "trusted". Any error is "not trusted".
func codexTrusted(path, dir string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	defer clear(b)
	var f struct {
		Projects map[string]struct {
			Trust string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if _, err := toml.Decode(string(b), &f); err != nil {
		return false
	}
	key, _ := trustKey(dir)
	return f.Projects[key].Trust == "trusted" || f.Projects[filepath.Clean(dir)].Trust == "trusted"
}

// errTrustPending: the person did not accept the trust in time; the agents
// already started stay, and agentbus up starts the others once it is.
var errTrustPending = errors.New("trust not accepted")

// trustGated are the clients whose folder trust gates a start.
var trustGated = map[string]bool{"claude-code": true, "codex": true}

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
		if !trustGated[client] {
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
	key, _ := trustKey(dir)
	clients := slices.Sorted(maps.Keys(gates))
	for _, c := range clients {
		where := gates[c] + "'s pane"
		if p := paneOf[gates[c]]; p != "" {
			where += " (" + p + ")"
		}
		fmt.Fprintf(d.Out, "%s asks whether you trust %s: answer it in %s. The other agents start once it is accepted (agentbus up %s is locked meanwhile).\n",
			clientLabel(c), key, where, tpl.Name)
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

// trustAskQID is a trust reminder's question id: the client it is about.
func trustAskQID(client string) string { return "trust-" + client }

// trustAskDef is the reminder (first-run-trust (b), the person's answer):
// the folder, where to answer, the choice by its label (the keys only as a
// hint: they change between client versions) and what trusting loads. Asked
// from the gate agent, whose pane shows the dialog. Nothing is written to a
// client's file.
func trustAskDef(client, agent, key, where string) bus.AskDef {
	label := clientLabel(client)
	hint := ""
	if client == "claude-code" {
		hint = " In Claude Code 2.1: ↓ then Enter (a hint: the keys change between versions)."
	}
	title := "Trust " + filepath.Base(key) + "?"
	if r := []rune(title); len(r) > 100 {
		title = string(r[:99]) + "…"
	}
	return bus.AskDef{From: agent, Title: title, Questions: []bus.AskQuestion{{
		ID:       trustAskQID(client),
		Header:   "Folder trust",
		Question: label + ` asks whether you trust this folder: in ` + where + `, choose "Yes, I trust this folder".`,
		Context: "Folder: " + key + ". Trusting lets " + label + " read, edit and run files there, and load the repository's own " +
			"settings, hooks and MCP servers: for a repository you did not write, look at it first." + hint +
			" This ask closes itself once the trust is recorded.",
		Options: []bus.AskOption{{Label: "Done in the pane", Description: "it also closes by itself once the trust is recorded"}},
	}}}
}

// trustReminders settles and opens the trust asks of tpl's gated clients:
// an open trust ask of this template's agents whose client now trusts dir is
// closed; each untrusted gated client without one gets one. It returns what
// closes the reminders once the trust is accepted. A broker error is a note:
// the line on the terminal still says where to answer.
func (d upDeps) trustReminders(tpl upTemplate, dir string, gates, paneOf map[string]string) func() {
	noop := func() {}
	if d.TrustAsks == nil || d.Trusted == nil {
		return noop
	}
	note := func(err error) {
		fmt.Fprintf(d.Out, "busmon ask not opened (%v): answer the dialog as said above\n", err)
	}
	ab, err := d.TrustAsks(tpl.Name)
	if err != nil {
		note(err)
		return noop
	}
	ctx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 5*time.Second)
	}
	c, cancel := ctx()
	asks, err := ab.Asks(c)
	cancel()
	if err != nil {
		note(err)
		return noop
	}
	ours := map[string]bool{}
	for _, t := range tpl.Tabs {
		ours[t.Name] = true
	}
	type open struct{ agent, id, qid string }
	openFor := map[string]open{} // client -> its open reminder
	for _, a := range asks {
		if a.State != "open" || !ours[a.From] {
			continue
		}
		for _, q := range a.Questions {
			if client, ok := strings.CutPrefix(q.ID, "trust-"); ok && q.Answer == nil {
				openFor[client] = open{a.From, a.ID, q.ID}
			}
		}
	}
	closeTrusted := func() {
		for client, o := range openFor {
			if !d.Trusted(client, dir) {
				continue
			}
			c, cancel := ctx()
			if _, err := ab.WithdrawQuestion(c, o.agent, o.id, o.qid, "trusted: recorded by "+clientLabel(client)); err == nil {
				delete(openFor, client)
			}
			cancel()
		}
	}
	closeTrusted()
	key, _ := trustKey(dir)
	for _, client := range slices.Sorted(maps.Keys(gates)) {
		if _, ok := openFor[client]; ok {
			continue
		}
		agent := gates[client]
		where := agent + "'s pane"
		if p := paneOf[agent]; p != "" {
			where += " (" + p + ")"
		}
		def := trustAskDef(client, agent, key, where)
		c, cancel := ctx()
		id, _, err := ab.Ask(c, def, "")
		cancel()
		if err != nil {
			note(err)
			continue
		}
		openFor[client] = open{agent, id, trustAskQID(client)}
	}
	return closeTrusted
}

func clientLabel(client string) string {
	switch client {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return client
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

// clientTrusted reads the trust of dir by client from its own file: Claude
// Code's ~/.claude.json (or $CLAUDE_CONFIG_DIR/.claude.json), Codex's
// $CODEX_HOME/config.toml (default ~/.codex). Other clients are not gated.
func clientTrusted(env func(string) string) func(client, dir string) bool {
	return func(client, dir string) bool {
		switch client {
		case "claude-code":
			base := env("CLAUDE_CONFIG_DIR")
			if base == "" {
				base = env("HOME")
			}
			return claudeTrusted(filepath.Join(base, ".claude.json"), dir)
		case "codex":
			base := env("CODEX_HOME")
			if base == "" {
				base = filepath.Join(env("HOME"), ".codex")
			}
			return codexTrusted(filepath.Join(base, "config.toml"), dir)
		}
		return true
	}
}
