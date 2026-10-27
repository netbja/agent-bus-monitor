package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// life is what is known of an agent's process.
type life int

const (
	lifeUnknown life = iota
	lifeAlive
	lifeDead
)

func (l life) String() string {
	switch l {
	case lifeAlive:
		return "alive"
	case lifeDead:
		return "dead"
	}
	return "unknown"
}

// snapLife judges an agent by its verified identity, the rule the dispatcher
// uses: the recorded pid runs with the recorded start time. A pid gone or
// reused is dead; no snapshot, no pid, or an unreadable process is unknown.
func snapLife(s bus.AgentSnapshot, has bool, procStart func(int) (uint64, error)) life {
	if !has || s.PID <= 0 || s.PIDStart == 0 || procStart == nil {
		return lifeUnknown
	}
	start, err := procStart(s.PID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return lifeDead
	case err != nil:
		return lifeUnknown
	case start != s.PIDStart:
		return lifeDead
	}
	return lifeAlive
}

// paneFacts is what herdr says of a tab's pane.
type paneFacts struct {
	Known bool   // herdr answered for this pane
	Agent string // the agent herdr detects in it, "" for none
	Shell bool   // its one foreground process is an interactive shell by its argv (no proof it is idle)
}

// relaunchVerdict is what up does with one template tab of an open workspace.
type relaunchVerdict string

const (
	verdictAlive    relaunchVerdict = "alive"
	verdictRunning  relaunchVerdict = "running"  // a tab that is not an agent: something runs in it
	verdictRelaunch relaunchVerdict = "relaunch" // in a new pane split from the tab's pane
	verdictCreate   relaunchVerdict = "create"   // no tab of that label: create it, then run
	verdictBusy     relaunchVerdict = "skipped (busy)"
	verdictUnknown  relaunchVerdict = "unknown"
	verdictMissing  relaunchVerdict = "missing" // no tab, and creating one was not asked for
)

// decideRelaunch is the pure rule. agent says the tab runs an agent-launch
// line; snap is that agent's life by its snapshot. A relaunch never types into
// an existing pane: whether a shell waits at its prompt cannot be proven from
// outside (a builtin read keeps its argv and can even keep a raw terminal), so
// the command runs in a new pane split from the tab's, and nothing is ever
// typed into the old one (closeOldPanes may close it once the new agent is
// proven). pane covers every pane of the tab: Shell only when all of them are
// plain shells.
func decideRelaunch(agent bool, snap life, hasPane bool, pane paneFacts, create bool) relaunchVerdict {
	if agent && snap == lifeAlive {
		return verdictAlive // alive, wherever its pane is
	}
	if !hasPane {
		if create {
			return verdictCreate
		}
		return verdictMissing
	}
	if !pane.Known {
		return verdictUnknown
	}
	if pane.Agent != "" {
		if !agent {
			return verdictRunning
		}
		if snap == lifeDead {
			return verdictBusy // another agent runs in its tab
		}
		return verdictAlive
	}
	if !pane.Shell {
		if agent {
			// Something runs in one of the tab's panes: maybe this agent, relaunched
			// before and not registered yet, under a name herdr does not detect.
			return verdictBusy
		}
		return verdictRunning
	}
	return verdictRelaunch
}

// launchRole reads `[env …] …/agent-launch <role> <project> …` and returns the
// role when the line launches one for this project.
func launchRole(command, project string) (string, bool) {
	f := strings.Fields(command)
	for i, w := range f {
		if filepath.Base(w) != "agent-launch" || i+2 >= len(f) {
			continue
		}
		if f[i+2] == project && bus.ValidName(f[i+1]) {
			return f[i+1], true
		}
	}
	return "", false
}

// sessionIDPattern: what a Claude Code session id looks like. Anything else is
// never appended to a command line typed into a shell.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

// resumeCommand is the tab's command resuming the agent's Claude Code
// session, or "" when it cannot: another client, no session, an odd id.
func resumeCommand(command string, s bus.AgentSnapshot, has bool) string {
	if !has || s.Session == "" || !sessionIDPattern.MatchString(s.Session) {
		return ""
	}
	if s.Client != "" && s.Client != "claude-code" {
		return ""
	}
	if strings.Contains(command, " --resume") {
		return ""
	}
	return command + " --resume " + s.Session
}

// shells are the foreground names of an idle pane.
var shells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "mksh", "tcsh", "csh", "nu"}

// interactiveFlags are the only arguments an idle interactive shell may carry.
var interactiveFlags = []string{"-l", "--login", "-i", "--interactive"}

// idleShell is affirmative evidence of a shell at its prompt: a shell started
// with nothing but login or interactive flags. A shell running a script
// (`bash x.sh`), a command (`bash -c 'read answer'`) or with any other
// argument is running work, and so is a process herdr reports without argv.
func idleShell(argv []string) bool {
	if len(argv) == 0 || !slices.Contains(shells, shellName(argv[0])) {
		return false
	}
	for _, a := range argv[1:] {
		if !slices.Contains(interactiveFlags, a) {
			return false
		}
	}
	return true
}

// workspacePanes maps a workspace's tab labels to the first pane of each,
// with herdr's detected agent: `tab list` and `pane list` for the workspace.
func workspacePanes(herdr func(args ...string) ([]byte, error), ws string) (map[string]paneRef, error) {
	out, err := herdr("tab", "list", "--workspace", ws)
	if err != nil {
		return nil, fmt.Errorf("herdr tab list: %w", err)
	}
	var tabs struct {
		Result struct {
			Tabs []struct {
				Label string `json:"label"`
				TabID string `json:"tab_id"`
			} `json:"tabs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &tabs); err != nil {
		return nil, fmt.Errorf("herdr tab list: %w", err)
	}
	out, err = herdr("pane", "list", "--workspace", ws)
	if err != nil {
		return nil, fmt.Errorf("herdr pane list: %w", err)
	}
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
				Agent  string `json:"agent"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, fmt.Errorf("herdr pane list: %w", err)
	}
	m := map[string]paneRef{}
	for _, t := range tabs.Result.Tabs {
		if ref, dup := m[t.Label]; dup {
			ref.Dup = true // two tabs of one label: which one holds the role is unknown
			m[t.Label] = ref
			continue
		}
		// Every pane of the tab is kept: a tab split by an earlier relaunch holds
		// the old pane and the new one, and the tab is judged on all of them.
		ref := m[t.Label]
		for _, p := range panes.Result.Panes {
			if p.TabID != t.TabID {
				continue
			}
			if ref.Pane == "" {
				ref.Pane = p.PaneID // the first pane: a relaunch splits from it
			}
			if ref.Agent == "" {
				ref.Agent = p.Agent // herdr sees an agent in one of them
			}
			ref.All = append(ref.All, p.PaneID)
		}
		m[t.Label] = ref
	}
	return m, nil
}

type paneRef struct {
	Pane, Agent string   // the first pane; the agent herdr sees in any pane of the tab
	All         []string // every pane of the tab
	Dup         bool     // more than one tab carries this label
}

// paneShell asks herdr for the pane's foreground processes: true when there is
// one, an interactive shell by its argv (idleShell). It only classifies a
// tab (its command is gone, so relaunch it in a new pane); it is never a
// licence to type into this pane. An error means unknown.
func paneShell(herdr func(args ...string) ([]byte, error), pane string) (bool, error) {
	out, err := herdr("pane", "process-info", "--pane", pane)
	if err != nil {
		return false, err
	}
	var v struct {
		Result struct {
			ProcessInfo struct {
				Foreground []struct {
					Argv []string `json:"argv"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return false, err
	}
	fg := v.Result.ProcessInfo.Foreground
	if len(fg) == 0 {
		return false, errors.New("no foreground process reported")
	}
	return len(fg) == 1 && idleShell(fg[0].Argv), nil
}

// relaunchOptions are up's flags for an open workspace.
type relaunchOptions struct {
	Resume, DryRun, Create bool
	Dir                    string         // the project directory a new pane starts in
	Prove                  *relaunchProof // nil: every old pane is left as it is
}

// relaunchProof is how up proves a relaunched agent before it closes the pane
// the dead one left (Bernard, 2026-10-27): the agents' snapshots, read once a
// second, at most Tries times.
type relaunchProof struct {
	Agents func() (map[string]bus.AgentSnapshot, error)
	Sleep  func(time.Duration)
	Tries  int
}

// oldPane is a relaunched agent's old pane, closed only once Role is proven
// in New (closeOldPanes).
type oldPane struct {
	Name, Role, Old, New string
	Was                  bus.AgentSnapshot
}

// relaunchOpen checks every tab of an open workspace and reruns the template
// command in each pane whose agent (or command) is gone. Master goes first,
// then the template order. It never types anything but the template command,
// into a pane it has just created, and touches only panes of workspace ws.
// The only pane it closes is a relaunched agent's old one: the pane its
// snapshot recorded, once the new agent is proven (closeOldPanes).
func relaunchOpen(herdr func(args ...string) ([]byte, error), snaps map[string]bus.AgentSnapshot, snapsOK bool,
	procStart func(int) (uint64, error), project, ws string, tpl upTemplate, o relaunchOptions, stdout io.Writer) error {
	panes, err := workspacePanes(herdr, ws)
	if err != nil {
		return err
	}
	dir := o.Dir
	tabs := slices.Clone(tpl.Tabs)
	slices.SortStableFunc(tabs, func(a, b upTab) int {
		ra, _ := launchRole(a.Command, project)
		rb, _ := launchRole(b.Command, project)
		switch {
		case ra == "master" && rb != "master":
			return -1
		case rb == "master" && ra != "master":
			return 1
		}
		return 0
	})
	var errs []error
	var olds []oldPane
	for _, t := range tabs {
		if strings.TrimSpace(t.Command) == "" {
			continue
		}
		role, isAgent := launchRole(t.Command, project)
		snap, has := snaps[role]
		has = has && isAgent && snapsOK
		sl := lifeUnknown
		if isAgent {
			sl = snapLife(snap, has, procStart)
		}
		ref, hasPane := panes[t.Name]
		if ref.Dup && !(isAgent && sl == lifeAlive) {
			fmt.Fprintf(stdout, "%s: unknown (several tabs are named %s; close the extra one)\n", t.Name, t.Name)
			continue
		}
		if hasPane && ref.Pane == "" && !(isAgent && sl == lifeAlive) {
			fmt.Fprintf(stdout, "%s: unknown (herdr lists the tab but no pane in it)\n", t.Name)
			continue
		}
		facts := paneFacts{}
		if hasPane {
			facts.Agent = ref.Agent
			facts.Known = true
			if ref.Agent == "" && !(isAgent && sl == lifeAlive) {
				// The tab's command is gone only if every pane is a plain shell: a
				// process in any of them may be the agent herdr does not detect.
				facts.Shell = true
				for _, p := range ref.All {
					shell, err := paneShell(herdr, p)
					if err != nil {
						facts.Known = false
						break
					}
					facts.Shell = facts.Shell && shell
				}
			}
		}
		v := decideRelaunch(isAgent, sl, hasPane, facts, o.Create)
		cmd := t.Command
		resumed := false
		if o.Resume && isAgent && (v == verdictRelaunch || v == verdictCreate) {
			if rc := resumeCommand(cmd, snap, has); rc != "" {
				cmd, resumed = rc, true
			}
		}
		// The old pane is closed only when it is the exact pane the dead agent's
		// snapshot recorded: the tab's other panes are never touched.
		old := ""
		if v == verdictRelaunch && isAgent && has && o.Prove != nil && slices.Contains(ref.All, snap.Pane) {
			old = snap.Pane
		}
		word := string(v)
		switch v {
		case verdictRelaunch, verdictCreate:
			word = "relaunched"
			if o.DryRun {
				word = "would relaunch"
			}
			switch {
			case v == verdictRelaunch && old != "" && o.DryRun:
				word += " (new pane; would close the old one " + old + " once the new one is proven)"
			case v == verdictRelaunch && old != "":
				word += " (new pane; the old one " + old + " closes once the new one is proven)"
			case v == verdictRelaunch:
				word += " (new pane; the old one is left as it is)"
			}
			if v == verdictCreate {
				word += " (new tab)"
			}
			if resumed {
				word += " (resumed)"
			} else if o.Resume && isAgent {
				word += " (fresh: no Claude Code session to resume)"
			}
		}
		if !o.DryRun {
			pane := ref.Pane
			if v == verdictCreate {
				pane, err = createTab(herdr, ws, t.Name)
				if err != nil {
					errs = append(errs, err)
					fmt.Fprintf(stdout, "%s: %v\n", t.Name, err)
					continue
				}
			}
			if v == verdictRelaunch {
				pane, err = splitPane(herdr, ref.Pane, dir)
				if err != nil {
					errs = append(errs, err)
					fmt.Fprintf(stdout, "%s: %v\n", t.Name, err)
					continue
				}
			}
			if v == verdictRelaunch || v == verdictCreate {
				if _, err := herdr("pane", "run", pane, cmd); err != nil {
					errs = append(errs, fmt.Errorf("herdr pane run (%s): %w", t.Name, err))
					fmt.Fprintf(stdout, "%s: relaunch failed: %v\n", t.Name, err)
					continue
				}
				if old != "" {
					olds = append(olds, oldPane{Name: t.Name, Role: role, Old: old, New: pane, Was: snap})
				}
			}
		}
		fmt.Fprintf(stdout, "%s: %s\n", t.Name, word)
	}
	if len(olds) > 0 {
		errs = append(errs, closeOldPanes(herdr, procStart, o.Prove, olds, stdout))
	}
	return errors.Join(errs...)
}

// closeOldPanes waits for each relaunched agent to be proven in its new pane,
// then closes the old pane it replaced, re-read right before: only a plain
// shell is closed. Closing is not typing. Anything else (no proof, busy,
// gone, unreadable) leaves the pane as it is and says so.
func closeOldPanes(herdr func(args ...string) ([]byte, error), procStart func(int) (uint64, error),
	p *relaunchProof, olds []oldPane, stdout io.Writer) error {
	proven := map[string]bool{}
	for try := 0; len(proven) < len(olds) && try < p.Tries; try++ {
		if try > 0 {
			p.Sleep(time.Second)
		}
		snaps, err := p.Agents()
		if err != nil {
			continue
		}
		for _, o := range olds {
			if s, ok := snaps[o.Role]; ok && provenIn(s, o, procStart) {
				proven[o.Role] = true
			}
		}
	}
	var errs []error
	for _, o := range olds {
		if !proven[o.Role] {
			fmt.Fprintf(stdout, "%s: old pane %s left as it is (no proof of the new agent in pane %s after %d s)\n", o.Name, o.Old, o.New, p.Tries)
			continue
		}
		shell, err := paneShell(herdr, o.Old)
		if err != nil {
			fmt.Fprintf(stdout, "%s: old pane %s left as it is (unreadable: %v)\n", o.Name, o.Old, err)
			continue
		}
		if !shell {
			fmt.Fprintf(stdout, "%s: old pane %s left as it is (no longer a plain shell)\n", o.Name, o.Old)
			continue
		}
		if _, err := herdr("pane", "close", o.Old); err != nil {
			errs = append(errs, fmt.Errorf("herdr pane close %s (%s): %w", o.Old, o.Name, err))
			fmt.Fprintf(stdout, "%s: old pane %s not closed: %v\n", o.Name, o.Old, err)
			continue
		}
		fmt.Fprintf(stdout, "%s: old pane %s closed\n", o.Name, o.Old)
	}
	return errors.Join(errs...)
}

// provenIn: the agent's verified identity is a new process, alive, bound in
// the pane the relaunch created.
func provenIn(s bus.AgentSnapshot, o oldPane, procStart func(int) (uint64, error)) bool {
	if s.Pane != o.New || s.PID <= 0 || s.PIDStart == 0 || s.PID == o.Was.PID && s.PIDStart == o.Was.PIDStart || procStart == nil {
		return false
	}
	start, err := procStart(s.PID)
	return err == nil && start == s.PIDStart
}

// createTab adds a missing tab to the workspace, unfocused, and returns its pane.
func createTab(herdr func(args ...string) ([]byte, error), ws, name string) (string, error) {
	out, err := herdr("tab", "create", "--workspace", ws, "--label", name, "--no-focus")
	if err != nil {
		return "", fmt.Errorf("herdr tab create %s: %w", name, err)
	}
	var tab struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &tab); err != nil || tab.Result.RootPane.PaneID == "" {
		return "", fmt.Errorf("herdr tab create %s: unexpected reply %s", name, strings.TrimSpace(string(out)))
	}
	return tab.Result.RootPane.PaneID, nil
}

// shellName is a shell's name from its argv[0] (a login shell has a leading -).
func shellName(argv0 string) string {
	return strings.TrimPrefix(filepath.Base(argv0), "-")
}

func itoa(n int) string { return strconv.Itoa(n) }

// splitPane opens a new pane beside pane, unfocused, in dir: a shell nobody
// has typed into, which is the only kind a relaunch writes to.
func splitPane(herdr func(args ...string) ([]byte, error), pane, dir string) (string, error) {
	args := []string{"pane", "split", pane, "--direction", "right"}
	if dir != "" {
		args = append(args, "--cwd", dir)
	}
	out, err := herdr(args...)
	if err != nil {
		return "", fmt.Errorf("herdr pane split: %w", err)
	}
	var v struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("herdr pane split: unexpected reply %s", strings.TrimSpace(string(out)))
	}
	return v.Result.Pane.PaneID, nil
}
