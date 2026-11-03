package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
)

// agentbus suspend (spec docs/superpowers/specs/2026-11-03-suspend-resume-
// design.md §4.1): per project, pause first (the suspend record, with the
// services' state read before anything stops), stop the dispatcher so no wake
// reserved before the pause can still be typed, wait for the quiet rule
// (suspendquiet.go), then read the snapshot's facts, stop the rest and write
// the snapshot. A wait that ends without quiet, a signal or a failed read
// cancels: the services step 2 stopped start again, then the suspend is lifted.

const suspendUsage = "usage: agentbus suspend [--project <p>] [--wait <d> | --no-wait] [--force] [--reason <text>] [--dry-run] [--yes] | agentbus suspend --status"

// suspendRound is the time between two readings of the quiet rule.
const suspendRound = 2 * time.Second

// suspendReadErrors is how many failed readings in a row cancel the wait.
const suspendReadErrors = 3

type suspendDeps struct {
	Getenv    func(string) string
	Bus       func(project string) (*bus.Bus, error)
	Projects  func(ctx context.Context) ([]string, error)
	Services  func() (service.Manager, string)
	Inspect   func(ctx context.Context, pane string) dispatch.Inspection
	ProcStart func(int) (uint64, error)
	Lock      func(project string) (func(), error)
	Hostname  func() (string, error)
	Boot      func() string
	StateDir  func(project string) string
	Person    func() string // the person's name (identity.as); "" = none recorded
	// Round and Settle are suspendRound and quietSettle; tests shorten them.
	Round, Settle time.Duration
	BeforeRound   func(n int)       // tests: called before each round's reading
	FactsErr      func(n int) error // tests: a round's reading fails when it returns an error
	Signals       func() (<-chan os.Signal, func())
	AgentCaller   func(ctx context.Context) (string, error)
	TTY           bool
	Stdin         io.Reader
}

func realSuspendDeps(host string) suspendDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	h := dispatch.Herdr{Bin: herdrBin(os.Getenv)}
	return suspendDeps{
		Getenv: os.Getenv,
		Bus: func(project string) (*bus.Bus, error) {
			r, err := bus.Connect(host)
			if err != nil {
				return nil, err
			}
			return bus.Open(r, project)
		},
		Projects: func(ctx context.Context) ([]string, error) {
			r, err := bus.ConnectContext(ctx, host)
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return suspendHostProjects(ctx, r, rolecfg.ProjectsDir(os.Getenv))
		},
		Services: func() (service.Manager, string) {
			return service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		},
		Inspect:   h.Inspect,
		ProcStart: dispatch.ProcStart,
		Lock: func(project string) (func(), error) {
			return upLock(upCacheDir(os.Getenv))(project, false)
		},
		Hostname: os.Hostname,
		Boot:     hostBoot,
		StateDir: paths.StateDir,
		Person: func() string {
			name, _ := personName(os.Getenv)
			return name
		},
		Round:  suspendRound,
		Settle: quietSettle,
		Signals: func() (<-chan os.Signal, func()) {
			c := make(chan os.Signal, 2)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			return c, func() { signal.Stop(c) }
		},
		AgentCaller: agentAncestor,
		TTY:         setup.IsTerminal(os.Stdin),
		Stdin:       os.Stdin,
	}
}

// suspendHostProjects is the host's projects (spec §2): the templates in the
// projects directory whose project the broker has.
func suspendHostProjects(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error) {
	ps, err := bus.Projects(ctx, c)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, p := range ps {
		have[p.Project] = true
	}
	var out []string
	tpls, _ := filepath.Glob(filepath.Join(projectsDir, "*.toml"))
	for _, t := range tpls {
		if name := strings.TrimSuffix(filepath.Base(t), ".toml"); bus.ValidName(name) && have[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// hostBoot identifies the running boot (spec §3): Linux's boot_id, macOS's
// kern.boottime; "" when it cannot be read.
func hostBoot() string {
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		return strings.TrimSpace(string(b))
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

type suspendOptions struct {
	wait   time.Duration
	force  bool
	reason string
	dryRun bool
	by     string
}

func runSuspend(ctx context.Context, args []string, explicitProject string, d suspendDeps, out, errw io.Writer) int {
	args, status := extractBool(args, "--status")
	args, noWait := extractBool(args, "--no-wait")
	args, force := extractBool(args, "--force")
	args, dryRun := extractBool(args, "--dry-run")
	args, yes := extractBool(args, "--yes")
	args, waitArg := extractFlag(args, "--wait")
	args, reason := extractFlag(args, "--reason")
	usage := func(msg string) int { fmt.Fprintf(errw, "suspend: %s\n%s\n", msg, suspendUsage); return 2 }
	if len(args) > 0 {
		return usage("unexpected " + strings.Join(args, " "))
	}
	o := suspendOptions{force: force, reason: reason, dryRun: dryRun, wait: config.Machine(d.Getenv).Duration("suspend.wait")}
	switch {
	case noWait && waitArg != "":
		return usage("give --wait or --no-wait, not both")
	case noWait:
		o.wait = 0
	case waitArg != "":
		w, err := time.ParseDuration(waitArg)
		if err != nil || w < 0 {
			return usage(fmt.Sprintf("--wait %q: want a duration like 10m", waitArg))
		}
		o.wait = w
	}
	projects, err := suspendTargets(ctx, explicitProject, d)
	if err != nil {
		fmt.Fprintf(errw, "suspend: %v\n", err)
		return 1
	}
	if status {
		return suspendStatus(ctx, projects, d, out, errw)
	}
	// The person's act (spec §4): an agent caller is refused whatever the
	// spelling of its command line, so no tool-rule deny has to match prose.
	if self := d.Getenv("AGENT_BUS_AGENT"); self != "" {
		fmt.Fprintf(errw, "suspend: putting the team to sleep is the person's act, not %s's (run it from your own shell)\n", self)
		return 1
	}
	if d.AgentCaller != nil {
		switch caller, err := d.AgentCaller(ctx); {
		case errors.Is(err, errNoAncestry):
		case err != nil:
			fmt.Fprintf(errw, "suspend: cannot check that no registered agent runs this (%v)\n", err)
			return 1
		case caller != "":
			fmt.Fprintf(errw, "suspend: putting the team to sleep is the person's act, and this runs under the agent %s\n", caller)
			return 1
		}
	}
	if o.by = d.Person(); !bus.ValidName(o.by) {
		fmt.Fprintln(errw, "suspend: no name recorded for you: agentbus identity set <your-name>, then run it again")
		return 2
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no project on this host to suspend")
		return 0
	}
	fmt.Fprintf(out, "suspend %s: pause now, let running turns end (up to %s), record a snapshot, stop the services\n",
		strings.Join(projects, ", "), o.wait)
	if !o.dryRun && !yes {
		if !d.TTY {
			fmt.Fprintln(errw, "suspend: no terminal to confirm on: run it again with --yes")
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
		if c := suspendProject(ctx, p, o, d, out, errw); c > code {
			code = c
		}
	}
	return code
}

// suspendTargets is the project named on the command line, else the host's.
func suspendTargets(ctx context.Context, explicitProject string, d suspendDeps) ([]string, error) {
	if explicitProject != "" {
		if !bus.ValidName(explicitProject) {
			return nil, fmt.Errorf("invalid project %q", explicitProject)
		}
		return []string{explicitProject}, nil
	}
	return d.Projects(ctx)
}

func suspendStatus(ctx context.Context, projects []string, d suspendDeps, out, errw io.Writer) int {
	code := 1
	for _, p := range projects {
		b, err := d.Bus(p)
		if err != nil {
			fmt.Fprintf(errw, "%s: %v\n", p, err)
			continue
		}
		rec, err := b.Pause(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(errw, "%s: %v\n", p, err)
		case rec == nil || !rec.Suspended():
			fmt.Fprintf(out, "%s: not suspended\n", p)
		default:
			code = 0
			fmt.Fprintf(out, "%s: suspended since %s by %s%s\n", p, time.UnixMilli(rec.At).Format("2006-01-02 15:04"), rec.By, reasonSuffix(rec.Reason))
			path := filepath.Join(d.StateDir(p), suspendSnapshotFile)
			if s, err := loadSnapshot(path); err == nil {
				fmt.Fprintf(out, "  snapshot %s: %s\n", path, s.counts())
			} else {
				fmt.Fprintf(out, "  no snapshot (%v): resume reads the bus as it is\n", err)
			}
		}
	}
	return code
}

// suspendProject runs §4.1 for one project; 0 suspended, 1 refused or
// cancelled.
func suspendProject(ctx context.Context, p string, o suspendOptions, d suspendDeps, out, errw io.Writer) int {
	say := func(format string, a ...any) { fmt.Fprintf(out, p+": "+format+"\n", a...) }
	fail := func(format string, a ...any) int { fmt.Fprintf(errw, p+": "+format+"\n", a...); return 1 }
	unlock, err := d.Lock(p)
	if err != nil {
		return fail("%v", err)
	}
	defer unlock()
	b, err := d.Bus(p)
	if err != nil {
		return fail("broker unreachable: %v (nothing can be paused)", err)
	}
	rec, err := b.Pause(ctx)
	if err != nil {
		return fail("reading the pause: %v", err)
	}
	snapPath := filepath.Join(d.StateDir(p), suspendSnapshotFile)
	if rec != nil && rec.Suspended() {
		if snap, err := loadSnapshot(snapPath); err == nil && snap.TakenAt >= rec.At {
			// Complete but for a service that did not stop (foureyes,
			// reviewer-codex): a rerun stops what still runs.
			mgr, _ := d.Services()
			if mgr == nil || !anyActive(mgr, p) {
				say("already suspended since %s", time.UnixMilli(rec.At).Format("15:04"))
				return 0
			}
			if o.dryRun {
				say("suspended, a service still running: would stop it and rewrite %s", snapPath)
				return 0
			}
			return finishStops(p, mgr, rec, snap, snapPath, out, errw)
		}
		say("suspended since %s without a current snapshot: completing it (Services kept as recorded)", time.UnixMilli(rec.At).Format("15:04"))
	} else {
		if intents, err := b.SwapIntents(ctx); err != nil {
			return fail("reading swap intents: %v", err)
		} else if len(intents) > 0 {
			var roles []string
			for r := range intents {
				roles = append(roles, r)
			}
			sort.Strings(roles)
			return fail("a swap, reload or retire of %s is in flight; retry once it ends (refused even with --force)", strings.Join(roles, ", "))
		}
	}
	if _, recs, err := b.ServiceStates(ctx); err == nil {
		if r := recs[service.Missions]; r.State == "busy" {
			say("missions is %s: the check or integration in progress is interrupted when it stops, and redone at its next start", r.Detail)
		}
	}
	mgr, why := d.Services()
	if mgr == nil {
		say("no service manager (%s): dispatch and missions run as herdr tabs; they stay, and paused they wake nobody", why)
	}
	if o.dryRun {
		say("would pause, wait up to %s for quiet, stop the services and write %s", o.wait, snapPath)
		return 0
	}
	if rec == nil || !rec.Suspended() {
		services, note := readServices(mgr, p)
		if note != "" {
			say("%s", note)
		}
		host, _ := d.Hostname()
		r, _, existed, err := b.Suspend(ctx, bus.SuspendSpec{By: o.by, Notify: suspendNotify(d.Getenv, p), Reason: o.reason,
			Host: host, Boot: d.Boot(), Services: services})
		if err != nil {
			return fail("pause: %v", err)
		}
		if existed {
			say("suspended meanwhile by %s: completing it", r.By)
		}
		rec = &r
	}
	raw, err := b.PauseRaw(ctx)
	if err != nil {
		return fail("reading the suspend back: %v", err)
	}
	if mgr != nil && rec.Services[service.Dispatch] {
		if st, err := mgr.State(p, service.Dispatch); err == nil && st.Active {
			if err := mgr.Stop(p, service.Dispatch); err != nil {
				return suspendCancel(ctx, p, b, raw, rec, mgr, o.by, "the dispatch service did not stop: "+err.Error(), d, out, errw)
			}
		}
	}
	busy, reason := suspendWait(ctx, p, b, mgr, o, d, out)
	if reason != "" || (len(busy) > 0 && !o.force) {
		if reason == "" {
			var names []string
			for _, a := range busy {
				names = append(names, a.Agent+" ("+a.Why+")")
			}
			reason = "still busy after " + o.wait.String() + ": " + strings.Join(names, ", ")
			fmt.Fprintf(errw, "%s: %s; run it again later, or with --force to cut their turns\n", p, reason)
		}
		return suspendCancel(ctx, p, b, raw, rec, mgr, o.by, reason, d, out, errw)
	}
	if len(busy) > 0 {
		say("--force: going on with %d agent(s) still busy; their turns are cut if the machine goes down", len(busy))
	}
	snap := readSnapshotFacts(ctx, p, b, rec, busy, d)
	return finishStops(p, mgr, rec, snap, snapPath, out, errw)
}

// finishStops stops every service still running, records what is actually
// stopped (a service that did not stop is recorded running), writes the
// snapshot, and fails while one still runs, so that a rerun, which then sees
// it active, stops it (foureyes, reviewer-codex).
func finishStops(p string, mgr service.Manager, rec *bus.PauseRecord, snap suspendSnapshot, snapPath string, out, errw io.Writer) int {
	var failed []string
	running := map[string]bool{}
	if mgr != nil {
		for _, k := range service.Kinds {
			st, err := mgr.State(p, k)
			if err != nil {
				snap.Unknown = append(snap.Unknown, "service "+k+": "+err.Error())
				failed = append(failed, k+" (unreadable: "+err.Error()+")")
				running[k] = true
				continue
			}
			if st.Active {
				if err := mgr.Stop(p, k); err != nil {
					failed = append(failed, k+" ("+err.Error()+")")
					running[k] = true
				}
			}
		}
	}
	if snap.Services == nil {
		snap.Services = map[string]snapService{}
	}
	for k, was := range rec.Services {
		snap.Services[k] = snapService{WasActive: was, Stopped: !running[k]}
	}
	if err := writeSnapshot(snapPath, snap); err != nil {
		fmt.Fprintf(errw, "%s: the project is suspended, but its snapshot was not written: %v (agentbus suspend -p %s writes it)\n", p, err, p)
		return 1
	}
	if len(failed) > 0 {
		fmt.Fprintf(errw, "%s: suspended, but %s did not stop; agentbus suspend -p %s stops it (snapshot %s)\n", p, strings.Join(failed, ", "), p, snapPath)
		return 1
	}
	fmt.Fprintf(out, "%s: suspended (%s); services stopped; snapshot %s\n", p, snap.counts(), snapPath)
	return 0
}

// anyActive reports whether a service of the project still runs (an
// unreadable state counts as running: a rerun then tries).
func anyActive(mgr service.Manager, p string) bool {
	for _, k := range service.Kinds {
		if st, err := mgr.State(p, k); err != nil || st.Active {
			return true
		}
	}
	return false
}

// readServices is each installed kind's state before anything stops; nil
// with a note when the manager is absent or cannot be read (resume then
// starts every installed kind).
func readServices(mgr service.Manager, p string) (map[string]bool, string) {
	if mgr == nil {
		return nil, ""
	}
	out := map[string]bool{}
	for _, k := range service.Kinds {
		st, err := mgr.State(p, k)
		if err != nil {
			return nil, "the service manager could not be read (" + err.Error() + "): resume will start every installed service"
		}
		if st.Installed {
			out[k] = st.Active
		}
	}
	return out, ""
}

// suspendNotify is whom the resume directive goes to: the project's
// escalation target, else master.
func suspendNotify(getenv func(string) string, p string) string {
	if paths, err := service.PathsFrom(getenv); err == nil {
		if env, err := service.LoadEnvFile(paths.EnvFile(p)); err == nil && bus.ValidName(env["AGENT_BUS_ESCALATE_TO"]) {
			return env["AGENT_BUS_ESCALATE_TO"]
		}
	}
	return "master"
}

// suspendWait runs the quiet rule's rounds; it returns the agents still busy
// when it stopped, and a reason when it stopped otherwise than by quiet or the
// deadline (a signal, failed readings).
func suspendWait(ctx context.Context, p string, b *bus.Bus, mgr service.Manager, o suspendOptions, d suspendDeps, out io.Writer) ([]busyAgent, string) {
	sigs, stop := d.Signals()
	defer stop()
	s := newQuietState()
	s.settle = d.Settle
	since := map[string]sinceMark{}
	deadline := time.Now().Add(o.wait)
	var goneAt int64
	var last []busyAgent
	shown := ""
	failures := 0
	for n := 1; ; n++ {
		if d.BeforeRound != nil {
			d.BeforeRound(n)
		}
		var f quietFacts
		var err error
		if d.FactsErr != nil {
			err = d.FactsErr(n)
		}
		if err == nil {
			f, err = readQuietFacts(ctx, p, b, mgr, d, since, &goneAt)
		}
		if err != nil {
			// A failed reading proves nothing (reviewer-codex): the quiet count
			// starts again, and the project stays busy until a reading succeeds.
			s.quiet = 0
			last = []busyAgent{{Agent: p, Why: "the bus could not be read (" + err.Error() + ")"}}
			if failures++; failures > suspendReadErrors {
				return last, "the bus could not be read: " + err.Error()
			}
		} else {
			failures = 0
			busy, quiet := s.round(f)
			if quiet {
				return nil, ""
			}
			last = busy
			if line := busyLine(busy); line != shown && line != "" {
				fmt.Fprintf(out, "%s: waiting for %s\n", p, line)
				shown = line
			}
		}
		// Zero wait still reads the quiet rule's minimum (two rounds). The
		// deadline never passes on less than quiet proven (reviewer-codex): a
		// last round quiet but not the one before it is still not quiet.
		if time.Now().After(deadline) && n >= quietReadyRounds {
			if len(last) == 0 {
				last = []busyAgent{{Agent: p, Why: "quiet in one round only, not two in a row"}}
			}
			return last, ""
		}
		select {
		case <-sigs:
			return last, "interrupted"
		case <-time.After(d.Round):
		}
	}
}

func busyLine(busy []busyAgent) string {
	var parts []string
	for _, a := range busy {
		parts = append(parts, a.Agent+": "+a.Why)
	}
	return strings.Join(parts, ", ")
}

// sinceMark carries an inspection's episode across rounds, as the
// dispatcher's observe pass does: the first reading of the same state by the
// same process.
type sinceMark struct {
	state string
	pid   int
	since int64
}

// readQuietFacts is one round's reading: the agents, their wake records, the
// suspend's own inspection of each verified pane, the processes proved gone,
// and the dispatcher's proved exit.
func readQuietFacts(ctx context.Context, p string, b *bus.Bus, mgr service.Manager, d suspendDeps, since map[string]sinceMark, goneAt *int64) (quietFacts, error) {
	now, err := b.ServerTime(ctx)
	if err != nil {
		return quietFacts{}, err
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return quietFacts{}, err
	}
	f := quietFacts{Now: now.UnixMilli(), Agents: snaps, Wakes: map[string]bus.WakeRecord{}, Obs: map[string]*bus.Observation{},
		Gone: map[string]bool{}, StaleAfter: config.Machine(d.Getenv).Duration("dispatch.turn_stale_after").Milliseconds()}
	for a, snap := range snaps {
		w, ok, err := b.Wake(ctx, a)
		if err != nil {
			return quietFacts{}, err
		}
		if ok {
			f.Wakes[a] = w
		}
		snap := snap
		if dispatch.ProcGone(&snap, d.ProcStart) {
			f.Gone[a] = true
			continue
		}
		if snap.Pane == "" || snap.PID == 0 || d.Inspect == nil {
			continue
		}
		in := d.Inspect(ctx, snap.Pane)
		if in.Err != nil || !dispatch.IdentityOK(&snap, in) {
			continue // a pane not running the agent's verified process proves nothing about it
		}
		// Only herdr's verified readiness is ready evidence (reviewer-codex):
		// idle or done without Ready reads unknown, which proves nothing.
		state := in.State
		if (state == "idle" || state == "done") && !in.Ready {
			state = "unknown"
		}
		o := &bus.Observation{Busy: in.State == "working" || in.Busy, State: state, At: f.Now, Since: f.Now, PID: snap.PID}
		if m, ok := since[a]; ok && m.state == o.State && m.pid == o.PID {
			o.Since = m.since
		}
		since[a] = sinceMark{o.State, o.PID, o.Since}
		f.Obs[a] = o
	}
	if *goneAt == 0 && mgr != nil {
		if st, err := mgr.State(p, service.Dispatch); err == nil && !st.Active {
			if alive, err := b.DispatchAlive(ctx); err == nil && !alive {
				*goneAt = f.Now
			}
		}
	}
	f.DispatchGoneAt = *goneAt
	return f, nil
}

// suspendCancel undoes a suspend that did not complete (spec §4.1 step 3):
// every service the record marks active and not active now starts again
// first; only then is the suspend lifted. A start that fails keeps the
// suspend (reviewer-codex): its Services are the only record of what to start.
func suspendCancel(ctx context.Context, p string, b *bus.Bus, raw string, rec *bus.PauseRecord, mgr service.Manager, by, why string, d suspendDeps, out, errw io.Writer) int {
	var failed []string
	if mgr != nil {
		paths, _ := service.PathsFrom(d.Getenv)
		for _, k := range service.Kinds {
			if !rec.Services[k] {
				continue
			}
			if st, err := mgr.State(p, k); err == nil && st.Active {
				continue
			}
			_ = service.Budget{Path: paths.BudgetFile(p, k)}.Reset()
			if err := mgr.Start(p, k); err != nil {
				failed = append(failed, k+" ("+err.Error()+")")
			}
		}
	}
	if len(failed) > 0 {
		line := fmt.Sprintf("could not start %s again: %s stays suspended; agentbus resume brings it back", strings.Join(failed, ", "), p)
		fmt.Fprintf(errw, "%s: %s\n", p, line)
		if _, err := b.PersonNotice(ctx, by, line); err != nil {
			fmt.Fprintf(errw, "%s: the notice was not written either: %v\n", p, err)
		}
		return 1
	}
	restored, _, err := b.SuspendCancel(ctx, raw, by, why)
	if err != nil {
		fmt.Fprintf(errw, "%s: the services run again, but the suspend could not be lifted: %v (agentbus resume lifts it)\n", p, err)
		return 1
	}
	if restored {
		fmt.Fprintf(errw, "%s: suspend cancelled (%s); the services run again and the previous pause is back\n", p, why)
	} else {
		fmt.Fprintf(errw, "%s: suspend cancelled (%s); the services run again and master is told\n", p, why)
	}
	return 1
}
