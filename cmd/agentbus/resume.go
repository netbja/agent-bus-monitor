package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
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
	// Lock is the project's up lock, taken without waiting and held from the
	// first read to the last write, so a suspend or another resume never
	// runs beside it; the nested up does not take it again. Nil: no lock.
	Lock func(project string) (func(), error)
	// resume --all (§7): the host's projects, whether a project's workspace
	// is open in a herdr session, and the confirmation.
	Projects      func(ctx context.Context) ([]string, error)
	WorkspaceOpen func(socket, project string) (bool, error)
	TTY           bool
	Stdin         io.Reader
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
			ud := realUpDepsIn(nil, log, log)
			ud.Lock = nil     // resume holds it
			ud.KeepEnv = true // a resume never changes the project's configuration
			return runUp(args, ud, log, log)
		},
		BootRoles:   templateAgents(os.Getenv),
		ProcStart:   dispatch.ProcStart,
		StateDir:    paths.StateDir,
		Person:      func() string { name, _ := personName(os.Getenv); return name },
		AgentCaller: agentAncestor,
		Lock: func(project string) (func(), error) {
			return upLock(upCacheDir(os.Getenv))(project, false)
		},
		Projects: realSuspendDeps(host).Projects,
		WorkspaceOpen: func(socket, project string) (bool, error) {
			id, err := findWorkspace(upDeps{Herdr: herdrIn(socket)}, project)
			return id != "", err
		},
		TTY:       setup.IsTerminal(os.Stdin),
		Stdin:     os.Stdin,
		ProofWait: resumeProofWait,
		Poll:      suspendRound,
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

// parseResumeArgs takes --partial and --yes (busmon's confirmation: resume
// asks nothing, so it only never lands in the message).
func parseResumeArgs(args []string) resumeOptions {
	args, partial := extractBool(args, "--partial")
	args, _ = extractBool(args, "--yes")
	return resumeOptions{partial: partial, message: strings.Join(args, " ")}
}

// suspendedResumeFirst runs the resume of a suspended project ahead of the
// identity guard, which would refuse it in the person's own herdr pane (no
// AGENT_BUS_AGENT), while its advice, exporting one, makes resume refuse the
// caller as an agent (foureyes). Only when the guard would refuse and the
// pause reads as a suspend: anything else, an unreadable pause included,
// takes the usual path. resume refuses an agent caller itself.
func suspendedResumeFirst(ctx context.Context, getenv func(string) string, args []string,
	readPause func(context.Context) (*bus.PauseRecord, error), full func(context.Context, []string) int) (bool, int) {
	if len(args) == 0 || args[0] != "resume" || identityGuard(getenv, args[0], args[1:]) == nil {
		return false, 0
	}
	rec, err := readPause(ctx)
	if err != nil || rec == nil || !rec.Suspended() {
		return false, 0
	}
	return true, full(ctx, args[1:])
}

func runSuspendedResume(ctx context.Context, p string, o resumeOptions, d resumeDeps, out, errw io.Writer) int {
	say := func(format string, a ...any) { fmt.Fprintf(out, p+": "+format+"\n", a...) }
	fail := func(format string, a ...any) int { fmt.Fprintf(errw, p+": "+format+"\n", a...); return 1 }
	// The person's act (spec §4), as suspend: an agent caller is refused.
	if why := refuseAgentCaller(ctx, d); why != "" {
		return fail("the project is suspended: %s", why)
	}
	by := d.Person()
	if !bus.ValidName(by) {
		return fail("no name recorded for you: agentbus identity set <your-name>, then run it again")
	}
	if d.Lock != nil {
		unlock, err := d.Lock(p)
		if err != nil {
			return fail("%v (a suspend or another resume of it is running); nothing done", err)
		}
		defer unlock()
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
	// keep is every way out that leaves the project suspended: said, and,
	// for a resume the workspace hook started, told to the person too, since
	// nobody reads the hook's log (reviewer-codex).
	keep := func(format string, a ...any) int {
		line := p + " stays suspended: " + fmt.Sprintf(format, a...)
		fmt.Fprintln(errw, line)
		if d.Getenv(resumeHookEnv) == "1" {
			if _, err := b.PersonNotice(ctx, by, line); err != nil {
				fmt.Fprintf(errw, "%s: the notice was not written: %v\n", p, err)
			}
		}
		return 1
	}
	// The agents that must come back: read before anything is done, since a
	// template that cannot be read would otherwise stand for none to wait for.
	roles, err := d.BootRoles(p)
	if err != nil {
		return keep("reading the template: %v: its agents cannot be checked; nothing done", err)
	}
	live, err := b.Agents(ctx)
	if err != nil {
		return keep("reading the agents: %v; nothing done", err)
	}
	snapPath := filepath.Join(d.StateDir(p), suspendSnapshotFile)
	snap, serr := loadSnapshot(snapPath)
	haveSnap := serr == nil && snap.TakenAt >= rec.At
	if !haveSnap {
		say("no current snapshot: the bus as it is now stands in")
	}

	required := requiredAgents(roles, snap, haveSnap, live, rec.At)

	// Services: the record's restart intent, else the snapshot's copy, else
	// every installed kind. One that cannot be read or started keeps the
	// suspend, --partial or not: --partial is about agents, and a project
	// without its dispatcher wakes nobody.
	var svcFailed []string
	mgr, why := d.Services()
	if mgr == nil {
		say("no service manager (%s): dispatch and missions run as herdr tabs, which up relaunches", why)
	} else {
		paths, _ := service.PathsFrom(d.Getenv)
		for _, k := range service.Kinds {
			st, err := mgr.State(p, k)
			if err != nil {
				fmt.Fprintf(errw, "%s: %s: %v\n", p, k, err)
				svcFailed = append(svcFailed, k)
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
				svcFailed = append(svcFailed, k)
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
	upRan := false
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
		upRan = true
		if code := d.Up([]string{p, "--resume", "--no-dispatch", "--no-missions"}, session, socket, out); code != 0 {
			fmt.Fprintf(errw, "%s: agentbus up %s --resume did not complete (exit %d)\n", p, p, code)
		}
	}

	// Proof: every required agent alive, its verified process. Without an up
	// nobody can come back: one reading, no wait.
	if !upRan {
		d.ProofWait = 0
	}
	missing := waitAlive(ctx, b, required, d)
	pops := missingPops(ctx, b, required, d)
	if len(svcFailed) > 0 {
		return keep("%s not running (agentbus --project %s service start <kind>, then agentbus resume again)", strings.Join(svcFailed, ", "), p)
	}
	if len(missing) > 0 && !o.partial {
		return keep("%s not back (agentbus resume again once they are, or agentbus resume --partial to lift it anyway)", strings.Join(missing, ", "))
	}
	summary := resumeSummary(p, rec, snap, haveSnap, required, missing, pops, o.message)
	restored, _, err := b.Unsuspend(ctx, raw, by, summary)
	if err != nil {
		return keep("lifting the suspend: %v", err)
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

// requiredAgents is every agent a resume waits for: the template's agent
// tabs, plus each boot agent (not a pop) the snapshot knows alive at the
// suspend, or, with no snapshot, each verified non-pop agent of the bus; an
// agent dormant at the suspend is not waited for (its last declaration a
// day before it). The template alone does not name an agent started by hand
// (reviewer-codex).
func requiredAgents(roles []string, snap suspendSnapshot, haveSnap bool, live map[string]bus.AgentSnapshot, at int64) []string {
	out := slices.Clone(roles)
	add := func(name string) {
		if s, ok := live[name]; ok && at-s.TS > bus.DormantAfter.Milliseconds() {
			return
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if haveSnap {
		for _, name := range slices.Sorted(maps.Keys(snap.Agents)) {
			if a := snap.Agents[name]; a.Tier != "pop" && !a.Gone {
				add(name)
			}
		}
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(live)) {
		if s := live[name]; s.Tier != "pop" && s.PID != 0 {
			add(name)
		}
	}
	return out
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
