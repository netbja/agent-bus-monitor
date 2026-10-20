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
	"strings"

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
	Shell bool   // its foreground is a shell and nothing else
}

// relaunchVerdict is what up does with one template tab of an open workspace.
type relaunchVerdict string

const (
	verdictAlive    relaunchVerdict = "alive"
	verdictRunning  relaunchVerdict = "running" // a tab that is not an agent: its command still runs
	verdictRelaunch relaunchVerdict = "relaunch"
	verdictCreate   relaunchVerdict = "create" // no tab of that label: create it, then run
	verdictBusy     relaunchVerdict = "skipped (busy)"
	verdictUnknown  relaunchVerdict = "unknown"
	verdictMissing  relaunchVerdict = "missing" // no tab, and creating one was not asked for
)

// decideRelaunch is the pure rule. agent says the tab runs an agent-launch
// line; snap is that agent's life by its snapshot. Only a pane whose
// foreground is a bare shell is ever typed into: herdr's agent in it, or any
// other foreground process, is not ours to interrupt.
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
			return verdictBusy
		}
		if snap == lifeDead {
			return verdictBusy // another agent runs where ours died
		}
		return verdictAlive
	}
	if !pane.Shell {
		if agent {
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

func isShell(name string) bool {
	return slices.Contains(shells, strings.TrimPrefix(filepath.Base(name), "-"))
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
		if _, dup := m[t.Label]; dup {
			continue // two tabs of one label: the first one counts
		}
		for _, p := range panes.Result.Panes {
			if p.TabID == t.TabID {
				m[t.Label] = paneRef{Pane: p.PaneID, Agent: p.Agent}
				break
			}
		}
	}
	return m, nil
}

type paneRef struct{ Pane, Agent string }

// paneShell asks herdr for the pane's foreground processes: true when they
// are all shells.
func paneShell(herdr func(args ...string) ([]byte, error), pane string) (bool, error) {
	out, err := herdr("pane", "process-info", "--pane", pane)
	if err != nil {
		return false, err
	}
	var v struct {
		Result struct {
			ProcessInfo struct {
				Foreground []struct {
					Name string   `json:"name"`
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
	for _, p := range fg {
		name := p.Name
		if len(p.Argv) > 0 {
			name = p.Argv[0]
		}
		if !isShell(name) {
			return false, nil
		}
	}
	return true, nil
}

// relaunchOptions are up's flags for an open workspace.
type relaunchOptions struct {
	Resume, DryRun, Create bool
}

// relaunchOpen checks every tab of an open workspace and reruns the template
// command in each pane whose agent (or command) is gone. Master goes first,
// then the template order. It never closes a pane, never types anything but
// the template command, and touches only panes of workspace ws.
func relaunchOpen(herdr func(args ...string) ([]byte, error), snaps map[string]bus.AgentSnapshot, snapsOK bool,
	procStart func(int) (uint64, error), project, ws string, tpl upTemplate, o relaunchOptions, stdout io.Writer) error {
	panes, err := workspacePanes(herdr, ws)
	if err != nil {
		return err
	}
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
		facts := paneFacts{}
		if hasPane {
			facts.Agent = ref.Agent
			facts.Known = true
			if ref.Agent == "" && !(isAgent && sl == lifeAlive) {
				shell, err := paneShell(herdr, ref.Pane)
				facts.Known, facts.Shell = err == nil, shell
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
		word := string(v)
		switch v {
		case verdictRelaunch, verdictCreate:
			word = "relaunched"
			if o.DryRun {
				word = "would relaunch"
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
			if v == verdictRelaunch || v == verdictCreate {
				if _, err := herdr("pane", "run", pane, cmd); err != nil {
					errs = append(errs, fmt.Errorf("herdr pane run (%s): %w", t.Name, err))
					fmt.Fprintf(stdout, "%s: relaunch failed: %v\n", t.Name, err)
					continue
				}
			}
		}
		fmt.Fprintf(stdout, "%s: %s\n", t.Name, word)
	}
	return errors.Join(errs...)
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
