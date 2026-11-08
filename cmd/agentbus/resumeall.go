package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus resume --all (spec 2026-11-03-suspend-resume-design.md §4, §7):
// every project of this host, a suspended one through §5, and one that is
// not suspended through the crash path: its installed services started, its
// dead agents relaunched only when its workspace is open, no pause touched.

const resumeAllUsage = "usage: agentbus resume --all [--dry-run] [--partial] [--yes]"

// refuseAgentCaller: waking a project is the person's act (spec §4), so an
// agent caller is refused whatever its command line. "" = the person.
func refuseAgentCaller(ctx context.Context, d resumeDeps) string {
	return agentcaller.Refuse(ctx, d.Getenv, d.AgentCaller, "waking a project", "agentbus resume")
}

func runResumeAll(ctx context.Context, args []string, d resumeDeps, out, errw io.Writer) int {
	args, _ = extractBool(args, "--all")
	args, dryRun := extractBool(args, "--dry-run")
	args, yes := extractBool(args, "--yes")
	o := parseResumeArgs(args)
	if o.message != "" {
		fmt.Fprintf(errw, "resume: unexpected %s\n%s\n", o.message, resumeAllUsage)
		return 2
	}
	if why := refuseAgentCaller(ctx, d); why != "" {
		fmt.Fprintf(errw, "resume: %s\n", why)
		return 1
	}
	if !bus.ValidName(d.Person()) {
		fmt.Fprintln(errw, "resume: no name recorded for you: agentbus identity set <your-name>, then run it again")
		return 2
	}
	projects, err := d.Projects(ctx)
	if err != nil {
		fmt.Fprintf(errw, "resume: broker unreachable: %v: start docker (docker compose up -d in the agent-bus checkout)\n", err)
		return 1
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no project on this host")
		return 0
	}
	if dryRun {
		return resumeAllDryRun(ctx, projects, d, out, errw)
	}
	fmt.Fprintf(out, "resume %s: a suspended project comes back (services, agents, then the pause lifted); any other has its stopped services started and its dead agents relaunched where its workspace is open\n",
		strings.Join(projects, ", "))
	if !yes {
		if !d.TTY {
			fmt.Fprintln(errw, "resume: no terminal to confirm on: run it again with --yes")
			return 2
		}
		fmt.Fprint(out, "Go on? [y/N] ")
		line, _ := bufio.NewReader(d.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "nothing done")
			return 1
		}
	}
	code := 0
	for _, p := range projects {
		c := resumeOne(ctx, p, o, d, out, errw)
		if c > code {
			code = c
		}
	}
	return code
}

// resumeOne takes §5 for a suspended project, §7 for any other. The project
// lock is taken before the pause is read and held through either path (given
// a copy of d without it, so neither takes it again): a suspend that lands
// between the choice and the work can never get the crash path
// (reviewer-codex).
func resumeOne(ctx context.Context, p string, o resumeOptions, d resumeDeps, out, errw io.Writer) int {
	if d.Lock != nil {
		unlock, err := d.Lock(p)
		if err != nil {
			fmt.Fprintf(errw, "%s: %v (a suspend or another resume of it is running); nothing done\n", p, err)
			return 1
		}
		defer unlock()
		d.Lock = nil
	}
	b, err := d.Bus(p)
	if err != nil {
		fmt.Fprintf(errw, "%s: broker unreachable: %v\n", p, err)
		return 1
	}
	rec, err := b.Pause(ctx)
	if err != nil {
		fmt.Fprintf(errw, "%s: reading the pause: %v\n", p, err)
		return 1
	}
	if rec != nil && rec.Suspended() {
		return runSuspendedResume(ctx, p, o, d, out, errw)
	}
	return runCrashResume(ctx, p, d, out, errw)
}

// runCrashResume is §7: a project that went down without a suspend.
func runCrashResume(ctx context.Context, p string, d resumeDeps, out, errw io.Writer) int {
	say := func(format string, a ...any) { fmt.Fprintf(out, p+": "+format+"\n", a...) }
	warn := func(format string, a ...any) { fmt.Fprintf(errw, p+": "+format+"\n", a...) }
	if d.Lock != nil {
		unlock, err := d.Lock(p)
		if err != nil {
			warn("%v (a suspend or another resume of it is running); nothing done", err)
			return 1
		}
		defer unlock()
	}
	b, err := d.Bus(p)
	if err != nil {
		warn("broker unreachable: %v", err)
		return 1
	}
	code := 0
	var started []string
	if mgr, why := d.Services(); mgr == nil {
		say("no service manager (%s): agentbus up %s relaunches its dispatch and missions tabs", why, p)
	} else {
		paths, _ := service.PathsFrom(d.Getenv)
		for _, k := range service.Kinds {
			st, err := mgr.State(p, k)
			if err != nil {
				warn("%s: %v", k, err)
				code = 1
				continue
			}
			if !st.Installed || st.Active {
				continue
			}
			_ = service.Budget{Path: paths.BudgetFile(p, k)}.Reset()
			if err := mgr.Start(p, k); err != nil {
				warn("%s did not start: %v (agentbus --project %s service start %s)", k, err, p, k)
				code = 1
				continue
			}
			started = append(started, k)
			say("%s started (it was stopped)", k)
		}
	}

	// The agents: those that should run and do not.
	var dead []string
	roles, rerr := d.BootRoles(p)
	if rerr != nil {
		warn("reading the template: %v: its agents are not checked", rerr)
		code = 1
	}
	live, lerr := b.Agents(ctx)
	if lerr != nil {
		warn("reading the agents: %v: they are not checked", lerr)
		code = 1
	}
	if rerr == nil && lerr == nil {
		now := int64(0)
		if t, err := b.ServerTime(ctx); err == nil {
			now = t.UnixMilli()
		}
		for _, r := range requiredAgents(roles, suspendSnapshot{}, false, live, now) {
			if s, ok := live[r]; !ok || !alive(&s, d.ProcStart) {
				dead = append(dead, r)
			}
		}
	}
	var relaunched, missing []string
	if len(dead) > 0 {
		missing = dead
		session := ""
		if paths, err := service.PathsFrom(d.Getenv); err == nil {
			if env, err := service.LoadEnvFile(paths.EnvFile(p)); err == nil {
				session = env["HERDR_SESSION"]
			}
		}
		var socket string
		open := false
		switch running, err := d.SessionRunning(session); {
		case session == "":
			warn("no herdr session is recorded for it")
		case err != nil || !running:
			warn("herdr session %s is not running", session)
		default:
			if socket, err = d.SessionSocket(session); err != nil {
				warn("herdr session %s: %v", session, err)
			} else if open, err = d.WorkspaceOpen(socket, p); err != nil {
				warn("herdr session %s: %v", session, err)
			}
		}
		if !open {
			// A crash never opens a workspace the person had closed.
			say("%s not running; its workspace is not open: agentbus up %s --resume relaunches them", strings.Join(dead, ", "), p)
		} else {
			if c := d.Up([]string{p, "--resume", "--no-dispatch", "--no-missions"}, session, socket, out); c != 0 {
				warn("agentbus up %s --resume did not complete (exit %d)", p, c)
				code = 1
			}
			missing = waitAlive(ctx, b, dead, d)
			for _, r := range dead {
				if !containsString(missing, r) {
					relaunched = append(relaunched, r)
				}
			}
		}
	}
	if len(started) == 0 && len(dead) == 0 && code == 0 {
		say("nothing to resume")
		return 0
	}
	summary := crashSummary(ctx, p, b, relaunched, missing, d)
	say("%s", summary)
	if len(missing) > 0 && code == 0 {
		code = 1
	}
	if len(relaunched) > 0 {
		to := suspendNotify(d.Getenv, p)
		if _, err := b.HumanCmd(ctx, d.Person(), to, cutText(summary, 4000)); err != nil {
			warn("%s was not told: %v", to, err)
			code = 1
		}
	}
	return code
}

// crashSummary says what a crash resume could not know, and the open work
// the bus holds now (ids and counts, as the snapshot reads them).
func crashSummary(ctx context.Context, p string, b *bus.Bus, relaunched, missing []string, d resumeDeps) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s resumed after a crash (no suspend).", p)
	if len(relaunched) > 0 {
		fmt.Fprintf(&s, " Relaunched: %s.", strings.Join(relaunched, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&s, " Not running: %s.", strings.Join(missing, ", "))
	}
	s.WriteString(" No suspend snapshot: turns running at the crash are lost; the open work below is what the bus holds now.")
	now := readSnapshotFacts(ctx, p, b, &bus.PauseRecord{}, nil, suspendDeps{Getenv: d.Getenv, ProcStart: d.ProcStart})
	waiting := 0
	for _, n := range now.Open.Waiting {
		waiting += int(n)
	}
	fmt.Fprintf(&s, " Open now: %d asks, %d gates, %d board tasks not done, %d messages waiting.",
		len(now.Open.Asks), len(now.Open.Gates), len(now.Open.Board), waiting)
	if len(now.Unknown) > 0 {
		fmt.Fprintf(&s, " Not read: %s.", strings.Join(now.Unknown, "; "))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir(p), suspendSnapshotFile)); err == nil {
		s.WriteString(" A snapshot from an earlier suspend was found and ignored.")
	}
	return s.String()
}

// resumeAllDryRun says, per project, what resume --all would do; it writes
// nothing.
func resumeAllDryRun(ctx context.Context, projects []string, d resumeDeps, out, errw io.Writer) int {
	code := 0
	for _, p := range projects {
		b, err := d.Bus(p)
		if err != nil {
			fmt.Fprintf(errw, "%s: broker unreachable: %v\n", p, err)
			code = 1
			continue
		}
		rec, err := b.Pause(ctx)
		if err != nil {
			fmt.Fprintf(errw, "%s: reading the pause: %v\n", p, err)
			code = 1
			continue
		}
		if rec != nil && rec.Suspended() {
			fmt.Fprintf(out, "%s: suspended by %s: would start its recorded services, relaunch its agents, then lift the pause\n", p, rec.By)
			continue
		}
		var stopped []string
		if mgr, _ := d.Services(); mgr != nil {
			for _, k := range service.Kinds {
				if st, err := mgr.State(p, k); err == nil && st.Installed && !st.Active {
					stopped = append(stopped, k)
				}
			}
		}
		if len(stopped) > 0 {
			fmt.Fprintf(out, "%s: not suspended: would start %s, and relaunch its dead agents if its workspace is open\n", p, strings.Join(stopped, ", "))
		} else {
			fmt.Fprintf(out, "%s: not suspended: would relaunch its dead agents if its workspace is open\n", p)
		}
	}
	return code
}

// cutText keeps at most n runes of s, marking a cut with an ellipsis.
func cutText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// resumeHookEnv marks a resume the workspace hook started: a project it
// leaves suspended is also told to the person by a for=person notice, since
// nobody reads the hook's log.
const resumeHookEnv = "AGENTBUS_RESUME_HOOK"

// withSuspendHook gives the workspace hook what it reads on a suspended
// project (spec §8).
func withSuspendHook(d upDeps) upDeps {
	d.SuspendPause = func(project string) (*bus.PauseRecord, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := bus.ConnectContext(ctx, "")
		if err != nil {
			return nil, err
		}
		defer c.Close()
		b, err := bus.Open(c, project)
		if err != nil {
			return nil, err
		}
		return b.Pause(ctx)
	}
	d.AutoResume = func() bool { return config.Machine(os.Getenv).Bool("suspend.auto_resume") }
	d.Boot = hostBoot
	d.Hostname = os.Hostname
	d.Person = func() string { name, _ := personName(os.Getenv); return name }
	d.SpawnResume = func(project, log string) error {
		return spawnDetachedLog(hookResumeArgv(project), log, resumeHookEnv+"=1")
	}
	return d
}

// hookResumeArgv is the resume the hook starts: exactly what the person
// would run, never --partial (spec §8).
func hookResumeArgv(project string) []string {
	return []string{"--project", project, "resume", "--yes"}
}

// spawnDetachedLog starts this binary with argv in its own session, its
// output appended to log (0600 in 0700), and does not wait for it.
func spawnDetachedLog(argv []string, log string, env ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, argv...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
