package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus resume on a suspended project (spec docs/superpowers/specs/2026-
// 11-03-suspend-resume-design.md §5): the services the record marks active
// start first, then up's own path relaunches the dead agents on their
// sessions inside the herdr session (never typing into an existing pane),
// then, once every agent of the template is alive, the suspend is lifted
// with one summary to master. A missing agent keeps the suspend unless
// --partial.

// resumeProofWait bounds how long resume waits for the agents to be alive.
const resumeProofWait = 90 * time.Second

// resumeKeep is how many set-aside snapshots stay.
const resumeKeep = 5

type resumeDeps struct {
	Getenv         func(string) string
	Bus            func(project string) (*bus.Bus, error)
	Services       func() (service.Manager, string)
	SessionRunning func(session string) (bool, error)
	SessionSocket  func(session string) (string, error)
	// Up runs agentbus up with these arguments inside the herdr session.
	Up          func(args []string, session, socket string, log io.Writer) int
	BootRoles   func(project string) ([]string, error) // the template's agent tabs
	ProcStart   func(int) (uint64, error)
	StateDir    func(project string) string
	Person      func() string
	AgentCaller func(ctx context.Context) (string, error)
	// ProofWait and Poll are resumeProofWait and suspendRound; tests shorten them.
	ProofWait, Poll time.Duration
}

func realResumeDeps(host string) resumeDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	return resumeDeps{
		Getenv: os.Getenv,
		Bus: func(project string) (*bus.Bus, error) {
			r, err := bus.Connect(host)
			if err != nil {
				return nil, err
			}
			return bus.Open(r, project)
		},
		Services:       func() (service.Manager, string) { return realSuspendDeps(host).Services() },
		SessionRunning: herdrSessionRunning,
		SessionSocket:  herdrSessionSocket,
		Up: func(args []string, session, socket string, log io.Writer) int {
			// up opens or relaunches inside herdr only: the three a herdr pane
			// gives a plugin, set for this process, as start does.
			for k, v := range map[string]string{"HERDR_ENV": "1", "HERDR_SESSION": session, "HERDR_SOCKET_PATH": socket} {
				_ = os.Setenv(k, v)
			}
			return runUp(args, realUpDepsIn(nil, log, log), log, log)
		},
		BootRoles:   templateAgents(os.Getenv),
		ProcStart:   dispatch.ProcStart,
		StateDir:    paths.StateDir,
		Person:      func() string { name, _ := personName(os.Getenv); return name },
		AgentCaller: agentAncestor,
		ProofWait:   resumeProofWait,
		Poll:        suspendRound,
	}
}

// templateAgents reads the project's template and returns its agent tabs'
// roles: the agents a resume must see alive.
func templateAgents(getenv func(string) string) func(project string) ([]string, error) {
	return func(project string) ([]string, error) {
		var tpl upTemplate
		if _, err := toml.DecodeFile(filepath.Join(rolecfg.ProjectsDir(getenv), project+".toml"), &tpl); err != nil {
			return nil, err
		}
		var roles []string
		for _, t := range tpl.Tabs {
			if role, ok := launchRole(t.Command, project); ok {
				roles = append(roles, role)
			}
		}
		return roles, nil
	}
}

type resumeOptions struct {
	partial bool
	message string
}

func runSuspendedResume(ctx context.Context, p string, o resumeOptions, d resumeDeps, out, errw io.Writer) int {
	say := func(format string, a ...any) { fmt.Fprintf(out, p+": "+format+"\n", a...) }
	fail := func(format string, a ...any) int { fmt.Fprintf(errw, p+": "+format+"\n", a...); return 1 }
	// The person's act (spec §4), as suspend: an agent caller is refused.
	if self := d.Getenv("AGENT_BUS_AGENT"); self != "" {
		return fail("the project is suspended: waking it is the person's act, not %s's (run agentbus resume from your own shell)", self)
	}
	if d.AgentCaller != nil {
		switch caller, err := d.AgentCaller(ctx); {
		case errors.Is(err, errNoAncestry):
		case err != nil:
			return fail("cannot check that no registered agent runs this (%v)", err)
		case caller != "":
			return fail("the project is suspended: waking it is the person's act, and this runs under the agent %s", caller)
		}
	}
	by := d.Person()
	if !bus.ValidName(by) {
		return fail("no name recorded for you: agentbus identity set <your-name>, then run it again")
	}
	b, err := d.Bus(p)
	if err != nil {
		return fail("broker unreachable: %v", err)
	}
	raw, err := b.PauseRaw(ctx)
	if err != nil {
		return fail("reading the pause: %v", err)
	}
	rec, err := b.Pause(ctx)
	if err != nil {
		return fail("reading the pause: %v", err)
	}
	if rec == nil || !rec.Suspended() {
		say("not suspended: nothing to resume")
		return 0
	}
	snapPath := filepath.Join(d.StateDir(p), suspendSnapshotFile)
	snap, serr := loadSnapshot(snapPath)
	haveSnap := serr == nil && snap.TakenAt >= rec.At
	if !haveSnap {
		say("no current snapshot: the bus as it is now stands in")
	}

	// Services: the record's restart intent, else the snapshot's copy, else
	// every installed kind.
	mgr, why := d.Services()
	if mgr == nil {
		say("no service manager (%s): dispatch and missions run as herdr tabs, which up relaunches", why)
	} else {
		paths, _ := service.PathsFrom(d.Getenv)
		for _, k := range service.Kinds {
			st, err := mgr.State(p, k)
			if err != nil {
				fmt.Fprintf(errw, "%s: %s: %v\n", p, k, err)
				continue
			}
			want := st.Installed
			switch {
			case rec.Services != nil:
				want = rec.Services[k]
			case haveSnap && snap.Services != nil:
				want = snap.Services[k].WasActive
			}
			if !want || st.Active {
				continue
			}
			_ = service.Budget{Path: paths.BudgetFile(p, k)}.Reset()
			if err := mgr.Start(p, k); err != nil {
				fmt.Fprintf(errw, "%s: %s did not start: %v (agentbus service start %s)\n", p, k, err, k)
				continue
			}
			say("%s started", k)
		}
	}

	// Agents: up's own path, inside the herdr session.
	session := ""
	if paths, err := service.PathsFrom(d.Getenv); err == nil {
		if env, err := service.LoadEnvFile(paths.EnvFile(p)); err == nil {
			session = env["HERDR_SESSION"]
		}
	}
	if session == "" && haveSnap {
		session = snap.Herdr.Session
	}
	switch running, err := d.SessionRunning(session); {
	case session == "":
		fmt.Fprintf(errw, "%s: no herdr session is recorded for it: agentbus up %s --resume relaunches its agents from herdr\n", p, p)
	case err != nil || !running:
		fmt.Fprintf(errw, "%s: herdr session %s is not running: start it (herdr --session %s), then run agentbus resume again\n", p, session, session)
	default:
		socket, err := d.SessionSocket(session)
		if err != nil {
			fmt.Fprintf(errw, "%s: herdr session %s: %v\n", p, session, err)
			break
		}
		if code := d.Up([]string{p, "--resume", "--no-dispatch", "--no-missions"}, session, socket, out); code != 0 {
			fmt.Fprintf(errw, "%s: agentbus up %s --resume did not complete (exit %d)\n", p, p, code)
		}
	}

	// Proof: every agent of the template alive, its verified process.
	roles, err := d.BootRoles(p)
	if err != nil {
		fmt.Fprintf(errw, "%s: reading the template: %v (the agents cannot be checked)\n", p, err)
	}
	missing := waitAlive(ctx, b, roles, d)
	pops := missingPops(ctx, b, roles, d)
	if len(missing) > 0 && !o.partial {
		fmt.Fprintf(errw, "%s: stays suspended: %s not back (agentbus resume again once they are, or agentbus resume --partial to lift it anyway)\n",
			p, strings.Join(missing, ", "))
		return 1
	}
	summary := resumeSummary(p, rec, snap, haveSnap, roles, missing, pops, o.message)
	restored, _, err := b.Unsuspend(ctx, raw, by, summary)
	if err != nil {
		return fail("lifting the suspend: %v", err)
	}
	if restored {
		say("resumed; the previous pause (%s, until %s) is back", rec.Prev.Reason, time.UnixMilli(rec.Prev.Until).Format("15:04"))
	} else {
		say("resumed; %s is told", rec.Notify)
	}
	if notifyMissing(rec.Notify, missing) {
		line := fmt.Sprintf("%s resumed with --partial while %s, who gets the resume directive, is not back", p, rec.Notify)
		if _, err := b.PersonNotice(ctx, by, line); err != nil {
			fmt.Fprintf(errw, "%s: %s (the notice was not written: %v)\n", p, line, err)
		}
	}
	for _, pop := range pops {
		say("%s", pop)
	}
	if haveSnap {
		if err := setAsideSnapshot(snapPath, snap.TakenAt); err != nil {
			fmt.Fprintf(errw, "%s: the snapshot was not set aside: %v\n", p, err)
		}
	}
	return 0
}

// waitAlive waits, at most ProofWait, until every role has a live verified
// process, and returns the ones that do not.
func waitAlive(ctx context.Context, b *bus.Bus, roles []string, d resumeDeps) []string {
	deadline := time.Now().Add(d.ProofWait)
	for {
		var missing []string
		snaps, err := b.Agents(ctx)
		for _, r := range roles {
			s, ok := snaps[r]
			if err != nil || !ok || !alive(&s, d.ProcStart) {
				missing = append(missing, r)
			}
		}
		if len(missing) == 0 || time.Now().After(deadline) {
			return missing
		}
		select {
		case <-ctx.Done():
			return missing
		case <-time.After(d.Poll):
		}
	}
}

// alive: a verified identity whose process runs with its recorded start.
func alive(s *bus.AgentSnapshot, procStart func(int) (uint64, error)) bool {
	if s.PID == 0 || s.PIDStart == 0 || procStart == nil {
		return false
	}
	start, err := procStart(s.PID)
	return err == nil && start == s.PIDStart
}

// missingPops names each pop that is not alive, with the command that brings
// it back on its session: resume never spawns a pop (spec §12, Q3).
func missingPops(ctx context.Context, b *bus.Bus, roles []string, d resumeDeps) []string {
	snaps, err := b.Agents(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for name, s := range snaps {
		s := s
		if s.Tier != "pop" || containsString(roles, name) || alive(&s, d.ProcStart) {
			continue
		}
		cmd := "agent-spawn " + name + " " + b.Project()
		if s.IdentSession != "" {
			cmd += " --resume " + s.IdentSession
		}
		out = append(out, "pop "+name+" not relaunched: "+cmd)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func notifyMissing(notify string, missing []string) bool { return containsString(missing, notify) }

// resumeSummary is the one directive master gets (spec §5 step 6): who came
// back, what was open, what is missing. Unsuspend cuts it to the bus's limit.
func resumeSummary(p string, rec *bus.PauseRecord, snap suspendSnapshot, haveSnap bool, roles, missing, pops []string, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s resumed after a suspend (%s to now, by %s).", p, time.UnixMilli(rec.At).Format("Jan 2 15:04"), rec.By)
	var back []string
	for _, r := range roles {
		if !containsString(missing, r) {
			back = append(back, r)
		}
	}
	if len(back) > 0 {
		fmt.Fprintf(&b, " Agents back: %s.", strings.Join(back, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, " Missing: %s.", strings.Join(missing, ", "))
	}
	for _, pop := range pops {
		fmt.Fprintf(&b, " %s.", pop)
	}
	if haveSnap {
		waiting := 0
		for _, n := range snap.Open.Waiting {
			waiting += int(n)
		}
		fmt.Fprintf(&b, " Open at the suspend: %d asks, %d gates, %d board tasks not done, %d messages waiting.",
			len(snap.Open.Asks), len(snap.Open.Gates), len(snap.Open.Board), waiting)
	} else {
		b.WriteString(" No snapshot: read the open work from the bus as it is now.")
	}
	if message != "" {
		b.WriteString(" " + message)
	}
	return b.String()
}

// setAsideSnapshot renames the snapshot suspend.<taken_at>.json and keeps
// the newest resumeKeep of them.
func setAsideSnapshot(path string, takenAt int64) error {
	dir := filepath.Dir(path)
	if err := os.Rename(path, filepath.Join(dir, fmt.Sprintf("suspend.%d.json", takenAt))); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "suspend.*.json"))
	sort.Strings(old) // the same number of digits for centuries: the name sorts by time
	for len(old) > resumeKeep {
		if err := os.Remove(old[0]); err != nil {
			return err
		}
		old = old[1:]
	}
	return nil
}
