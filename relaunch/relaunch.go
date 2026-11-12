// Package relaunch is the relaunch core (self-repair watcher spec, part 7,
// step 1): it judges a template tab of an open workspace and reruns the
// template command in a new pane split from it, then proves the new agent and
// closes the old pane. It never types into an existing pane. Its callers are
// agentbus up --resume and reload (the CLI entry, which keeps its herdr-pane
// guard and up's lock) and, once built, the dispatch service (ServiceHerdr).
package relaunch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Life is what is known of an agent's process.
type Life int

const (
	LifeUnknown Life = iota
	LifeAlive
	LifeDead
)

func (l Life) String() string {
	switch l {
	case LifeAlive:
		return "alive"
	case LifeDead:
		return "dead"
	}
	return "unknown"
}

// SnapLife judges an agent by its verified identity, the rule the dispatcher
// uses: the recorded pid runs with the recorded start time. A pid gone or
// reused is dead; no snapshot, no pid, or an unreadable process is unknown.
// Gone is PidListedNoMore: dispatch.ProcStart's ErrNoProcess does not wrap
// fs.ErrNotExist, and reading it as unknown made a tab whose old client
// herdr still showed read alive, never waited on (reload of 2026-11-09).
func SnapLife(s bus.AgentSnapshot, has bool, procStart func(int) (uint64, error)) Life {
	if !has || s.PID <= 0 || s.PIDStart == 0 || procStart == nil {
		return LifeUnknown
	}
	start, err := procStart(s.PID)
	switch {
	case err != nil && PidListedNoMore(err):
		return LifeDead
	case err != nil:
		return LifeUnknown
	case start != s.PIDStart:
		return LifeDead
	}
	return LifeAlive
}

// PaneFacts is what herdr says of a tab's pane.
type PaneFacts struct {
	Known bool   // herdr answered for this pane
	Agent string // the agent herdr detects in it, "" for none
	Shell bool   // its one foreground process is an interactive shell by its argv (no proof it is idle)
}

// Verdict is what up does with one template tab of an open workspace.
type Verdict string

const (
	VerdictAlive    Verdict = "alive"
	VerdictRunning  Verdict = "running"  // a tab that is not an agent: something runs in it
	VerdictRelaunch Verdict = "relaunch" // in a new pane split from the tab's pane
	VerdictCreate   Verdict = "create"   // no tab of that label: create it, then run
	VerdictBusy     Verdict = "skipped (busy)"
	VerdictUnknown  Verdict = "unknown"
	VerdictMissing  Verdict = "missing" // no tab, and creating one was not asked for
)

// Decide is the pure rule. agent says the tab runs an agent-launch
// line; snap is that agent's Life by its snapshot. A relaunch never types into
// an existing pane: whether a shell waits at its prompt cannot be proven from
// outside (a builtin read keeps its argv and can even keep a raw terminal), so
// the command runs in a new pane split from the tab's, and nothing is ever
// typed into the old one (closeOldPanes may close it once the new agent is
// proven). pane covers every pane of the tab: Shell only when all of them are
// plain shells.
func Decide(agent bool, snap Life, hasPane bool, pane PaneFacts, create bool) Verdict {
	if agent && snap == LifeAlive {
		return VerdictAlive // alive, wherever its pane is
	}
	if !hasPane {
		if create {
			return VerdictCreate
		}
		return VerdictMissing
	}
	if !pane.Known {
		return VerdictUnknown
	}
	if pane.Agent != "" {
		if !agent {
			return VerdictRunning
		}
		if snap == LifeDead {
			return VerdictBusy // another agent runs in its tab
		}
		return VerdictAlive
	}
	if !pane.Shell {
		if agent {
			// Something runs in one of the tab's panes: maybe this agent, relaunched
			// before and not registered yet, under a name herdr does not detect.
			return VerdictBusy
		}
		return VerdictRunning
	}
	return VerdictRelaunch
}

// LaunchRole reads `[env …] …/agent-launch <role> <project> …` and returns the
// role when the line launches one for this project.
func LaunchRole(command, project string) (string, bool) {
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

// ResumeCommand is the tab's command resuming the agent's session, or ""
// when it cannot: a client whose session source is not verified
// (rolecfg.ResumeVerified), no session, an odd id.
func ResumeCommand(command string, s bus.AgentSnapshot, has bool) string {
	if !has || s.Session == "" || !sessionIDPattern.MatchString(s.Session) {
		return ""
	}
	if s.Client != "" && !rolecfg.ResumeVerified(s.Client) {
		return ""
	}
	if strings.Contains(command, " --resume") {
		return ""
	}
	return command + " --resume " + s.Session
}

// ProvedSession is the session a relaunch of the agent may resume: the one
// proved with its process (IdentSession), for a client whose session source
// is verified (self-repair step 5), else "".
func ProvedSession(s bus.AgentSnapshot) string {
	if !rolecfg.ResumeVerified(s.Client) || !bus.ValidSessionID(s.IdentSession) {
		return ""
	}
	return s.IdentSession
}

// shells are the foreground names of an idle pane.
var shells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "mksh", "tcsh", "csh", "nu"}

// interactiveFlags are the only arguments an idle interactive shell may carry.
var interactiveFlags = []string{"-l", "--login", "-i", "--interactive"}

// IdleShell is affirmative evidence of a shell at its prompt: a shell started
// with nothing but login or interactive flags. A shell running a script
// (`bash x.sh`), a command (`bash -c 'read answer'`) or with any other
// argument is running work, and so is a process herdr reports without argv.
func IdleShell(argv []string) bool {
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

// WorkspacePanes maps a workspace's tab labels to the first pane of each,
// with herdr's detected agent: `tab list` and `pane list` for the workspace.
func WorkspacePanes(herdr func(args ...string) ([]byte, error), ws string) (map[string]PaneRef, error) {
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
	m := map[string]PaneRef{}
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

type PaneRef struct {
	Pane, Agent string   // the first pane; the agent herdr sees in any pane of the tab
	All         []string // every pane of the tab
	Dup         bool     // more than one tab carries this label
}

// PaneShell asks herdr for the pane's foreground processes: true when there is
// one, an interactive shell by its argv (IdleShell). It only classifies a
// tab (its command is gone, so relaunch it in a new pane); it is never a
// licence to type into this pane. An error means unknown.
func PaneShell(herdr func(args ...string) ([]byte, error), pane string) (bool, error) {
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
	return len(fg) == 1 && IdleShell(fg[0].Argv), nil
}

// Options are up's flags for an open workspace.
type Options struct {
	Resume, DryRun, Create bool
	Dir                    string // the project directory a new pane starts in
	Prove                  *Proof // nil: every old pane is left as it is
	// CloseBusmon: a relaunched busmon's old shell is closed (start-busmon-pane):
	// only up on an open workspace sets it.
	CloseBusmon bool
	// Verdicts, when set, receives each tab's verdict by tab name: a caller
	// that relaunches one role (reload) fails on anything but a relaunch.
	Verdicts map[string]Verdict
	// Held, when set, is an agent role's intent read right before it is
	// relaunched (self-repair spec 4.1): a held one (a self-repair, a reload,
	// a swap or a retire) is skipped, by its kind ("" a swap). up sets it;
	// reload, which holds its own intent, does not.
	Held func(role string) (kind string, held bool)
}

// TabJudgement is one template tab of an open workspace, judged.
type TabJudgement struct {
	V       Verdict
	Note    string // why an unknown is unknown, when it is decided before the panes are read
	Role    string
	IsAgent bool
	Snap    bus.AgentSnapshot
	Has     bool // a snapshot of an agent tab, read successfully
	Ref     PaneRef
}

// JudgeTab reads herdr's facts for tab t and applies Decide: the
// one rule both up and reload's relaunch use.
func JudgeTab(herdr func(args ...string) ([]byte, error), panes map[string]PaneRef, t Tab, project string,
	snaps map[string]bus.AgentSnapshot, snapsOK bool, procStart func(int) (uint64, error), create bool) TabJudgement {
	role, isAgent := LaunchRole(t.Command, project)
	snap, has := snaps[role]
	has = has && isAgent && snapsOK
	sl := LifeUnknown
	if isAgent {
		sl = SnapLife(snap, has, procStart)
	}
	j := TabJudgement{Role: role, IsAgent: isAgent, Snap: snap, Has: has}
	ref, hasPane := panes[t.Name]
	j.Ref = ref
	if ref.Dup && !(isAgent && sl == LifeAlive) {
		j.V, j.Note = VerdictUnknown, "several tabs are named "+t.Name+"; close the extra one"
		return j
	}
	if hasPane && ref.Pane == "" && !(isAgent && sl == LifeAlive) {
		j.V, j.Note = VerdictUnknown, "herdr lists the tab but no pane in it"
		return j
	}
	facts := PaneFacts{}
	if hasPane {
		facts.Agent = ref.Agent
		facts.Known = true
		if ref.Agent == "" && !(isAgent && sl == LifeAlive) {
			// The tab's command is gone only if every pane is a plain shell: a
			// process in any of them may be the agent herdr does not detect.
			facts.Shell = true
			for _, p := range ref.All {
				shell, err := PaneShell(herdr, p)
				if err != nil {
					facts.Known = false
					break
				}
				facts.Shell = facts.Shell && shell
			}
		}
	}
	j.V = Decide(isAgent, sl, hasPane, facts, create)
	return j
}

// Proof is how up proves a relaunched agent before it closes the pane
// the dead one left (the person, 2026-10-27): the agents' snapshots, read once a
// second, at most Tries times.
type Proof struct {
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

// Open checks every tab of an open workspace and reruns the template
// command in each pane whose agent (or command) is gone. Master goes first,
// then the template order. It never types anything but the template command,
// into a pane it has just created, and touches only panes of workspace ws.
// The only pane it closes is a relaunched agent's old one: the pane its
// snapshot recorded, once the new agent is proven (closeOldPanes).
func Open(herdr func(args ...string) ([]byte, error), snaps map[string]bus.AgentSnapshot, snapsOK bool,
	procStart func(int) (uint64, error), project, ws string, tpl Template, o Options, stdout io.Writer) error {
	panes, err := WorkspacePanes(herdr, ws)
	if err != nil {
		return err
	}
	dir := o.Dir
	tabs := slices.Clone(tpl.Tabs)
	slices.SortStableFunc(tabs, func(a, b Tab) int {
		ra, _ := LaunchRole(a.Command, project)
		rb, _ := LaunchRole(b.Command, project)
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
		j := JudgeTab(herdr, panes, t, project, snaps, snapsOK, procStart, o.Create)
		if o.Verdicts != nil {
			o.Verdicts[t.Name] = j.V
		}
		if j.Note != "" {
			fmt.Fprintf(stdout, "%s: %s (%s)\n", t.Name, j.V, j.Note)
			continue
		}
		role, isAgent, snap, has, ref, v := j.Role, j.IsAgent, j.Snap, j.Has, j.Ref, j.V
		if o.Held != nil && isAgent && (v == VerdictRelaunch || v == VerdictCreate) {
			if kind, held := o.Held(role); held {
				if kind == "" {
					kind = "swap"
				}
				fmt.Fprintf(stdout, "%s: skipped (%s in progress)\n", t.Name, kind)
				continue
			}
		}
		cmd := t.Command
		resumed := false
		if o.Resume && isAgent && (v == VerdictRelaunch || v == VerdictCreate) {
			if rc := ResumeCommand(cmd, snap, has); rc != "" {
				cmd, resumed = rc, true
			}
		}
		// The old pane is closed only when it is the exact pane the dead agent's
		// snapshot recorded: the tab's other panes are never touched.
		old := ""
		if v == VerdictRelaunch && isAgent && has && o.Prove != nil && slices.Contains(ref.All, snap.Pane) {
			old = snap.Pane
		}
		// busmon's tab (start-busmon-pane, the person's answers of 2026-11-06):
		// its old pane is closed with no proof, only when it is the tab's one
		// pane, re-read as a plain shell right before the close.
		busmonOld, busmonMany := "", 0
		if v == VerdictRelaunch && o.CloseBusmon && IsBusmonTab(t, project) {
			if len(ref.All) == 1 {
				busmonOld = ref.All[0]
			} else {
				busmonMany = len(ref.All)
			}
		}
		word := string(v)
		switch v {
		case VerdictRelaunch, VerdictCreate:
			word = "relaunched"
			if o.DryRun {
				word = "would relaunch"
			}
			switch {
			case v == VerdictRelaunch && old != "" && o.DryRun:
				word += " (new pane; would close the old one " + old + " once the new one is proven)"
			case v == VerdictRelaunch && old != "":
				word += " (new pane; the old one " + old + " closes once the new one is proven)"
			case busmonOld != "" && o.DryRun:
				word += " (new pane; would close the old one " + busmonOld + " if it is still a plain shell)"
			case busmonOld != "":
				word += " (new pane; the old one " + busmonOld + " closes if it is still a plain shell)"
			case busmonMany > 0:
				word += fmt.Sprintf(" (new pane; the old ones are left as they are: the tab has %d panes)", busmonMany)
			case v == VerdictRelaunch:
				word += " (new pane; the old one is left as it is)"
			}
			if v == VerdictCreate {
				word += " (new tab)"
			}
			if resumed {
				word += " (resumed)"
			} else if o.Resume && isAgent {
				word += " (fresh: no session to resume)"
			}
		}
		if !o.DryRun {
			pane := ref.Pane
			if v == VerdictCreate {
				pane, err = createTab(herdr, ws, t.Name)
				if err != nil {
					errs = append(errs, err)
					fmt.Fprintf(stdout, "%s: %v\n", t.Name, err)
					continue
				}
			}
			if v == VerdictRelaunch {
				pane, err = splitPane(herdr, ref.Pane, dir)
				if err != nil {
					errs = append(errs, err)
					fmt.Fprintf(stdout, "%s: %v\n", t.Name, err)
					continue
				}
			}
			if v == VerdictRelaunch || v == VerdictCreate {
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
		if busmonOld != "" && !o.DryRun {
			if err := closeIfShell(herdr, t.Name, busmonOld, stdout); err != nil {
				errs = append(errs, err)
			}
		}
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
	p *Proof, olds []oldPane, stdout io.Writer) error {
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
		shell, err := PaneShell(herdr, o.Old)
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

// closeIfShell closes pane once it is re-read, right before, as one plain
// interactive shell; anything else leaves it as it is and says why. Closing
// is not typing: nothing is ever typed into it.
func closeIfShell(herdr func(args ...string) ([]byte, error), name, pane string, stdout io.Writer) error {
	shell, err := PaneShell(herdr, pane)
	if err != nil {
		fmt.Fprintf(stdout, "%s: old pane %s left as it is (unreadable: %v)\n", name, pane, err)
		return nil
	}
	if !shell {
		fmt.Fprintf(stdout, "%s: old pane %s left as it is (no longer a plain shell)\n", name, pane)
		return nil
	}
	if _, err := herdr("pane", "close", pane); err != nil {
		fmt.Fprintf(stdout, "%s: old pane %s not closed: %v\n", name, pane, err)
		return fmt.Errorf("herdr pane close %s (%s): %w", pane, name, err)
	}
	fmt.Fprintf(stdout, "%s: old pane %s closed\n", name, pane)
	return nil
}

// IsBusmonTab: the template tab named busmon that runs this project's busmon
// (its first word's base name busmon, --project <project>). No other tab.
func IsBusmonTab(t Tab, project string) bool {
	if t.Name != "busmon" {
		return false
	}
	w, err := rolecfg.ShellSplit(t.Command)
	if err != nil || len(w) == 0 || filepath.Base(w[0]) != "busmon" {
		return false
	}
	for i, a := range w[1:] {
		if a == "--project="+project || a == "--project" && i+2 < len(w) && w[i+2] == project {
			return true
		}
	}
	return false
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

// Tab is one tab of a project template.
type Tab struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

// Template is a project template (~/.config/agentbus/projects/<p>.toml).
type Template struct {
	Name       string `toml:"name"`
	WorkingDir string `toml:"working_dir"`
	Tabs       []Tab  `toml:"tabs"`
}

// PidListedNoMore: a pid's start could not be read because /proc, readable
// here, lists no such pid: dispatch.ProcStart's ErrNoProcess, which it returns
// only after that same check (backup-live-check: it does not wrap
// fs.ErrNotExist), or a bare fs.ErrNotExist. Any other error (no /proc, a
// failed read) proves nothing: the project is refused.
func PidListedNoMore(err error) bool {
	if errors.Is(err, dispatch.ErrNoProcess) {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, serr := os.Stat("/proc/self/stat")
	return serr == nil
}
