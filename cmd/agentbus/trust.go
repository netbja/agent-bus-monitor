package main

import (
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
	p, ok := tabProfile(command, env)
	if !ok {
		return ""
	}
	return p.Client
}

// tabExecutable is the program a template tab's agent runs (installer-no-
// client): the builtin client's binary or the adapter's executable, "" when
// the tab launches no agent or the role does not resolve.
func tabExecutable(command string, env func(string) string) string {
	p, ok := tabProfile(command, env)
	if !ok {
		return ""
	}
	return rolecfg.ClientExecutable(p)
}

// tabProfile resolves the role a tab's agent-launch names.
func tabProfile(command string, env func(string) string) (rolecfg.Profile, bool) {
	// The template quotes its words (rolecfg.ShellJoin): split them back as a
	// shell would, so a path with a space stays one word (reviewer-codex).
	words, err := rolecfg.ShellSplit(command)
	if err != nil {
		words = strings.Fields(strings.ReplaceAll(command, "'", ""))
	}
	manifest, role := env("ROLES_TOML"), ""
	for i, w := range words {
		if m, ok := strings.CutPrefix(w, "ROLES_TOML="); ok {
			manifest = m
		}
		if filepath.Base(w) == "agent-launch" && i+1 < len(words) {
			role = words[i+1]
		}
	}
	if role == "" || manifest == "" {
		return rolecfg.Profile{}, false
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		return rolecfg.Profile{}, false
	}
	p, err := m.Resolve(role)
	if err != nil {
		return rolecfg.Profile{}, false
	}
	return p, true
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
