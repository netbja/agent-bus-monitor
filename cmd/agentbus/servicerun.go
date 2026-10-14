package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
)

// serviceRunDeps is what `agentbus service run` needs beyond the bus,
// injected for tests.
type serviceRunDeps struct {
	Paths       service.Paths
	Host        string
	Heartbeat   time.Duration // how often the state is published
	Standby     time.Duration // how often a standby service checks whether the lease is free (default 15s)
	HerdrPing   func(context.Context) error
	RunDispatch func(ctx context.Context, b *bus.Bus, escalateTo string, onLease func(bool), log io.Writer) int
	RunMissions func(ctx context.Context, b *bus.Bus, cfg mission.RunConfig) error
	Now         func() time.Time
}

// lastError keeps the last line that reports an error, so a failed service
// can say why in busmon.
type lastError struct {
	w    io.Writer
	mu   sync.Mutex
	line string
	part []byte
}

func (l *lastError) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.part = append(l.part, p...)
	for {
		i := bytes.IndexByte(l.part, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(l.part[:i]))
		l.part = l.part[i+1:]
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "error") || strings.Contains(low, `"error"`) || strings.Contains(low, "failed") {
			l.line = line
		}
	}
	l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lastError) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.line
}

// incidentWindow: the conductor's latest incident stays in its state this long.
const incidentWindow = 10 * time.Minute

// lastIncident is the conductor's latest logged incident and when it came.
type lastIncident struct {
	mu   sync.Mutex
	text string
	at   time.Time
}

func (l *lastIncident) set(s string) {
	l.mu.Lock()
	l.text, l.at = s, time.Now()
	l.mu.Unlock()
}

func (l *lastIncident) recent(window time.Duration) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.text == "" || time.Since(l.at) > window {
		return ""
	}
	return l.text + " (" + time.Since(l.at).Truncate(time.Second).String() + " ago)"
}

// serviceState is what the heartbeat publishes; the loop updates it.
type serviceState struct {
	mu                   sync.Mutex
	state, detail, error string
}

func (s *serviceState) set(state, detail, err string) {
	s.mu.Lock()
	s.state, s.detail, s.error = state, detail, err
	s.mu.Unlock()
}

func (s *serviceState) get() (string, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.detail, s.error
}

// serviceRun is one run of a project's service under its manager. The exit
// code is the restart rule both managers follow: 0 is final (stopped, project
// gone, another instance, invalid configuration, budget spent), 1 is a failure
// worth a restart, counted against the budget.
func serviceRun(ctx context.Context, b *bus.Bus, kind string, args []string, d serviceRunDeps, logw io.Writer) int {
	p := b.Project()
	logf := func(format string, a ...any) {
		fmt.Fprintf(logw, time.Now().Format("2006-01-02 15:04:05 ")+"service: "+format+"\n", a...)
	}
	budget := service.Budget{Path: d.Paths.BudgetFile(p, kind)}
	rec := func(state, detail, errText string) bus.ServiceRecord {
		return bus.ServiceRecord{State: state, Detail: detail, Error: errText, PID: os.Getpid(), Host: d.Host, Log: d.Paths.LogFile(p, kind)}
	}
	// publish writes the state; false means the project is gone.
	publish := func(state, detail, errText string) bool {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		ok, err := b.PutServiceState(pctx, kind, rec(state, detail, errText))
		if err != nil {
			logf("publish state: %v", err)
			return true // the broker is down, the project is not known to be gone
		}
		return ok
	}

	giveUpDetail := fmt.Sprintf("%d failed starts in %v; run agentbus up %s to try again", service.BudgetStarts, service.BudgetWindow, p)
	// fail records a failed run. The one that spends the budget gives up here,
	// in this process: systemd's start limit refuses the next start, so no
	// later run would get to say it.
	fail := func(why string) int {
		_ = budget.Failed(d.Now())
		if budget.Spent(d.Now()) {
			logf("failed: %s; gave up: %s", why, giveUpDetail)
			publish("gave-up", giveUpDetail, why)
			return 0
		}
		logf("failed: %s; the manager restarts it", why)
		publish("failed", "restarting", why)
		return 1
	}
	if installed, err := b.ServicesInstalled(ctx); err != nil {
		logf("cannot read %s: %v", bus.ServicesKey(p), err)
		return fail(err.Error())
	} else if !installed {
		logf("project gone: %s has no services marker (deleted, or never set up with agentbus up); exiting", p)
		return 0
	}
	if budget.Spent(d.Now()) {
		logf("gave up: %s", giveUpDetail)
		publish("gave-up", giveUpDetail, "")
		return 0
	}

	var repo string
	escalate := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--repo" && i+1 < len(args):
			repo = args[i+1]
			i++
		case args[i] == "--escalate-to" && i+1 < len(args) && bus.ValidName(args[i+1]):
			escalate = args[i+1]
			i++
		default:
			msg := fmt.Sprintf("invalid configuration: unexpected argument %q", args[i])
			logf("%s", msg)
			publish("failed", "", msg)
			return 0
		}
	}
	if kind == service.Missions {
		if out, err := exec.Command("git", "-C", repo, "rev-parse", "--git-dir").CombinedOutput(); repo == "" || err != nil {
			msg := fmt.Sprintf("invalid configuration: --repo %q is not a git repository (%s)", repo, strings.TrimSpace(string(out)))
			logf("%s", msg)
			publish("failed", "", msg)
			return 0
		}
	}

	errs := &lastError{w: logw}
	incident := &lastIncident{}
	st := &serviceState{}
	st.set("starting", "", "")
	publish("starting", "", "")
	logf("starting %s for %s (pid %d)", kind, p, os.Getpid())

	// attempt runs the loop once, with the heartbeat beside it.
	attempt := func() (code int, runErr error, gone bool) {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var goneFlag, leased atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { // heartbeat: state every d.Heartbeat; a vanished marker ends the run
			defer wg.Done()
			t := time.NewTicker(d.Heartbeat)
			defer t.Stop()
			for {
				if kind == service.Dispatch && leased.Load() {
					if err := d.HerdrPing(runCtx); err != nil && runCtx.Err() == nil {
						st.set("waiting-herdr", "herdr does not answer; no agent is woken until it does", err.Error())
					} else {
						st.set("ready", "holds the dispatch lease", "")
					}
				}
				if kind == service.Missions {
					if state, detail := missionsActivity(runCtx, b); state != "" {
						st.set(state, detail, incident.recent(incidentWindow))
					}
				}
				state, detail, e := st.get()
				if runCtx.Err() != nil {
					return
				}
				if !publish(state, detail, e) {
					logf("project deleted: the services marker is gone; stopping")
					goneFlag.Store(true)
					cancel()
					return
				}
				select {
				case <-runCtx.Done():
					return
				case <-t.C:
				}
			}
		}()

		switch kind {
		case service.Dispatch:
			code = d.RunDispatch(runCtx, b, escalate, func(held bool) {
				leased.Store(held)
				if !held { // not ready again before it re-acquires
					st.set("starting", "re-acquiring the dispatch lease", "")
				}
			}, errs)
		case service.Missions:
			cfg := mission.DefaultRunConfig()
			cfg.StateHome = d.Paths.StateHome + "/agentbus"
			cfg.Repo = repo
			if escalate != "" {
				cfg.EscalateTo = escalate
			}
			cfg.Log = func(format string, a ...any) {
				line := fmt.Sprintf(format, a...)
				fmt.Fprintf(errs, "%s%s\n", time.Now().Format("15:04:05 "), line)
				incident.set(line) // the conductor logs only incidents
			}
			runErr = d.RunMissions(runCtx, b, cfg)
		}
		cancel()
		wg.Wait()
		return code, runErr, goneFlag.Load()
	}

	standbyEvery := d.Standby
	if standbyEvery <= 0 {
		standbyEvery = 15 * time.Second
	}
	var code int
	var runErr error
	for {
		var gone bool
		code, runErr, gone = attempt()
		if gone {
			return 0
		}
		if ctx.Err() != nil {
			logf("stopped")
			publish("stopped", "stopped by the service manager (agentbus down, service stop, or logout)", "")
			return 0
		}
		if !(kind == service.Missions && errors.Is(runErr, mission.ErrAlreadyRunning) ||
			kind == service.Dispatch && code == 0 && anotherDispatcher(ctx, b)) {
			break
		}
		// Another instance (an old herdr tab, another machine) holds the lease:
		// wait, then take over when it is free. Not a restart loop: this
		// process stays and only polls the lease.
		logf("another instance holds the lease; waiting in standby")
		st.set("starting", "", "")
		for {
			if !publish("standby", "another instance holds the lease (a herdr tab, or another machine); taking over when it is free", "") {
				logf("project deleted while in standby; exiting")
				return 0
			}
			select {
			case <-ctx.Done():
				logf("stopped")
				publish("stopped", "stopped by the service manager (agentbus down, service stop, or logout)", "")
				return 0
			case <-time.After(standbyEvery):
			}
			if !leaseHeld(ctx, b, kind) {
				break
			}
		}
		logf("the lease is free; taking over")
	}

	switch {
	case kind == service.Dispatch && code == 0, kind == service.Missions && runErr == nil:
		if ok, _ := b.ServicesInstalled(ctx); !ok {
			logf("project gone; exiting")
			return 0
		}
		logf("exited")
		publish("stopped", "exited", "")
		return 0
	}
	// A failure: the project may have gone meanwhile (the conductor's lease
	// lapses with it), which is final, not a restart.
	if ok, err := b.ServicesInstalled(ctx); err == nil && !ok {
		logf("project gone; exiting")
		return 0
	}
	why := errs.last()
	if runErr != nil {
		why = runErr.Error()
	}
	if why == "" {
		why = fmt.Sprintf("exit %d", code)
	}
	return fail(why)
}

// leaseHeld: someone holds the lease of this service kind. An unreadable
// lease counts as held, so a broker hiccup never starts a second instance.
func leaseHeld(ctx context.Context, b *bus.Bus, kind string) bool {
	alive, err := b.DispatchAlive(ctx)
	if kind == service.Missions {
		alive, err = b.ConductorAlive(ctx)
	}
	return err != nil || alive
}

func anotherDispatcher(ctx context.Context, b *bus.Bus) bool {
	alive, err := b.DispatchAlive(ctx)
	return err == nil && alive
}

// missionsActivity says whether the conductor has work: busy while a change
// is active and its revision not complete, idle otherwise.
func missionsActivity(ctx context.Context, b *bus.Bus) (state, detail string) {
	s := mission.Store{B: b}
	names, err := s.Changes(ctx)
	if err != nil {
		return "", ""
	}
	var busy []string
	for _, c := range names {
		r, ok, err := s.Change(ctx, c)
		if err == nil && ok && r.State == "active" && r.CompletedRev != r.Rev {
			busy = append(busy, fmt.Sprintf("%s rev %d", c, r.Rev))
		}
	}
	if len(busy) == 0 {
		return "idle", "waiting for missions (no active change)"
	}
	return "busy", "running " + strings.Join(busy, ", ")
}
