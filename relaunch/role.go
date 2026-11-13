package relaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Deps is what Role reads and runs, injected.
type Deps struct {
	Herdr           func(args ...string) ([]byte, error) // the herdr CLI on the project's session
	IgnoreWorkspace string                               // never taken for the project's workspace
	Agents          func(project string) (map[string]bus.AgentSnapshot, error)
	ProcStart       func(int) (uint64, error) // nil off Linux: a snapshot then decides nothing
	Sleep           func(time.Duration)       // nil: time.Sleep
	Home            string                    // the template's ~
	// Fence, when set, is Options.Fence: the service entry's check, right
	// before the split, that the relaunch is still its own.
	Fence func(role string) error
}

// SettleTries reads of the role's tab, SettleEvery apart (20 s), before a
// relaunch gives up on a tab that still reads busy after the old exit.
const (
	SettleTries = 40
	SettleEvery = 500 * time.Millisecond
)

// Role relaunches role's template tab of project's open workspace: the
// settle loop, the split and the run (Open), the proof, the old pane's close.
// session is the proved session to resume ("" starts fresh). The caller has
// checked where it runs and taken the project lock (the CLI entry: reload;
// the service entry: ServiceRole); Role checks neither.
func Role(d Deps, project, role, session string, tpl Template, stdout io.Writer) error {
	tpl.Tabs = slices.DeleteFunc(slices.Clone(tpl.Tabs), func(t Tab) bool { // the caller's template stays whole
		r, ok := LaunchRole(t.Command, project)
		return !ok || r != role
	})
	if len(tpl.Tabs) != 1 {
		return fmt.Errorf("the template has %d tabs launching %s, want 1", len(tpl.Tabs), role)
	}
	ws, err := FindWorkspace(d.Herdr, project, d.IgnoreWorkspace)
	if err != nil {
		return err
	}
	if ws == "" {
		return fmt.Errorf("the workspace of %s is not open", project)
	}
	if d.Agents == nil {
		return fmt.Errorf("no bus to prove the new agent on")
	}
	snaps, err := d.Agents(project)
	if err != nil {
		return err
	}
	// The relaunch resumes the session the reload chose (the proved one),
	// not the snapshot's unverified Session.
	if s, ok := snaps[role]; ok {
		s.Session = session
		snaps[role] = s
	}
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	// The old process is gone, but herdr's view of its tab lags its exit: its
	// agent detection and the pane's foreground process still show the
	// client for a moment, which Decide reads as busy. Wait, bounded,
	// for the tab to be what a relaunch needs (no agent, every pane a plain
	// shell), as up --resume finds it seconds later. The rule is not relaxed
	// and nothing is typed while waiting.
	tab := tpl.Tabs[0]
	for try := 1; ; try++ {
		panes, err := WorkspacePanes(d.Herdr, ws)
		if err != nil {
			return err
		}
		j := JudgeTab(d.Herdr, panes, tab, project, snaps, true, d.ProcStart, true)
		if j.V == VerdictRelaunch || j.V == VerdictCreate {
			break
		}
		if (j.V != VerdictBusy && j.V != VerdictUnknown) || try >= SettleTries {
			why := string(j.V)
			if j.Note != "" {
				why += " (" + j.Note + ")"
			}
			return fmt.Errorf("its tab %s still reads %s %s after the old process exited (%d reads); nothing was relaunched",
				tab.Name, why, SettleEvery*time.Duration(try-1), try) // the first read comes before any wait
		}
		sleep(SettleEvery)
	}
	verdicts := map[string]Verdict{}
	o := Options{Resume: session != "", Create: true, Verdicts: verdicts, Fence: d.Fence,
		Dir:   os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Home, 1)),
		Prove: &Proof{Agents: func() (map[string]bus.AgentSnapshot, error) { return d.Agents(project) }, Sleep: sleep, Tries: 60}}
	if err := Open(d.Herdr, snaps, true, d.ProcStart, project, ws, tpl, o, stdout); err != nil {
		return err
	}
	if v := verdicts[tab.Name]; v != VerdictRelaunch && v != VerdictCreate {
		return fmt.Errorf("its tab %s read %s at the relaunch; nothing was relaunched", tab.Name, v)
	}
	return nil
}

// FindWorkspace is the id of the project's open workspace, "" when none is
// open; ignore is never taken (a workspace herdr made for a session agentbus
// start launched).
func FindWorkspace(herdr func(args ...string) ([]byte, error), project, ignore string) (string, error) {
	out, err := herdr("workspace", "list")
	if err != nil {
		return "", fmt.Errorf("herdr workspace list: %w", err)
	}
	var v struct {
		Result struct {
			Workspaces []struct {
				Label       string `json:"label"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("herdr workspace list: %w", err)
	}
	for _, w := range v.Result.Workspaces {
		if w.Label == project && w.WorkspaceID != ignore {
			return w.WorkspaceID, nil
		}
	}
	return "", nil
}

// ErrBusy: another up of the project (or the watcher) holds the lock.
var ErrBusy = errors.New("another agentbus up of this project is running")

// ProjectLock takes ~/.cache/agentbus/up-<project>.lock: one up per project at a
// time, so two ups never split or fill one workspace together. wait=false
// gives up with ErrBusy while another holds it. Any failure to establish
// the lock is an error: up then touches no pane.
func ProjectLock(cacheDir string) func(project string, wait bool) (func(), error) {
	return func(project string, wait bool) (func(), error) {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("lock directory: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(cacheDir, "up-"+project+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("lock file: %w", err)
		}
		how := syscall.LOCK_EX
		if !wait {
			how |= syscall.LOCK_NB
		}
		if err := syscall.Flock(int(f.Fd()), how); err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrBusy
			}
			return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}, nil
	}
}

// CacheDir is ~/.cache/agentbus (or $XDG_CACHE_HOME/agentbus): where the
// project lock lives.
func CacheDir(env func(string) string) string {
	if x := env("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "agentbus")
	}
	return filepath.Join(env("HOME"), ".cache", "agentbus")
}

// ReadTemplate reads project's template from the projects directory.
func ReadTemplate(env func(string) string, project string) (Template, error) {
	var tpl Template
	if _, err := toml.DecodeFile(filepath.Join(rolecfg.ProjectsDir(env), project+".toml"), &tpl); err != nil {
		return Template{}, fmt.Errorf("template: %w", err)
	}
	return tpl, nil
}

// ErrNoSession: the service's environment names no herdr session.
var ErrNoSession = errors.New("the services' env file names no herdr session (HERDR_SESSION or HERDR_SOCKET_PATH): run agentbus up from the project's herdr session")

// ServiceHerdr is the herdr CLI as the dispatch service reaches it (self-repair
// spec 4.1, the service entry): a service runs in no pane, so it talks to the
// session its env file names (service/env.go carries HERDR_SESSION,
// HERDR_SOCKET_PATH and the binary from the shell agentbus up ran in). It
// refuses when the environment names no session: it never guesses one.
func ServiceHerdr(env func(string) string) (func(args ...string) ([]byte, error), error) {
	if env("HERDR_SESSION") == "" && env("HERDR_SOCKET_PATH") == "" {
		return nil, ErrNoSession
	}
	bin := env("HERDR_BIN_PATH")
	if bin == "" {
		bin = env("HERDR_BIN")
	}
	if bin == "" {
		bin = "herdr"
	}
	return func(args ...string) ([]byte, error) {
		return exec.Command(bin, args...).Output() // the service's own environment: the session it names
	}, nil
}

// ServiceHerdrContext is ServiceHerdr whose calls take a context: killed at
// its end (its own process group), so a herdr that hangs never holds the
// caller (the dispatcher's pass, pop-retire-auto; reviewer-codex).
func ServiceHerdrContext(env func(string) string) (func(ctx context.Context, args ...string) ([]byte, error), error) {
	if env("HERDR_SESSION") == "" && env("HERDR_SOCKET_PATH") == "" {
		return nil, ErrNoSession
	}
	bin := env("HERDR_BIN_PATH")
	if bin == "" {
		bin = env("HERDR_BIN")
	}
	if bin == "" {
		bin = "herdr"
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, bin, args...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		c.Cancel = func() error { // the whole group, a child it started included
			if c.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		c.WaitDelay = time.Second // a child holding the output pipe does not hold the call
		return c.Output()
	}, nil
}

// ServiceRole is the service entry of a relaunch: no herdr-pane check (a
// service is in none), but up's project lock, so it never splits a tab while
// an up runs; it waits for nothing (another holder: ErrBusy). Nothing calls it
// yet: the watcher's R1 does, once built (self-repair spec part 7, step 7).
func ServiceRole(d Deps, lock func(project string, wait bool) (func(), error), project, role, session string, tpl Template, stdout io.Writer) error {
	if lock == nil {
		return errors.New("no project lock: the service never relaunches without up's lock")
	}
	unlock, err := lock(project, false)
	if err != nil {
		return err
	}
	defer unlock()
	return Role(d, project, role, session, tpl, stdout)
}
