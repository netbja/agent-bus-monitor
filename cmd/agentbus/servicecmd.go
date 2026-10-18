package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
)

const serviceUsage = "usage: agentbus --project <p> service status|start|stop|restart|remove [dispatch|missions] | service logs [dispatch|missions] [-n 50] | service list | service remove --all"

// serviceCmdDeps: the manager (nil when none is usable, Why says why), the
// user's directories and, for state reads, the project's bus.
type serviceCmdDeps struct {
	Manager service.Manager
	Why     string
	Paths   service.Paths
	Bus     func(project string) (*bus.Bus, error)
}

func realServiceCmdDeps(host string) serviceCmdDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	bin := service.ManagerBin(runtime.GOOS, os.Getenv)
	m, why := service.Detect(runtime.GOOS, service.CommandRunner(bin), os.Getenv)
	return serviceCmdDeps{Manager: m, Why: why, Paths: paths, Bus: func(project string) (*bus.Bus, error) {
		c, err := bus.Connect(host)
		if err != nil {
			return nil, err
		}
		return bus.Open(c, project)
	}}
}

// projectKinds are the kinds installed for project, in start order.
func projectKinds(m service.Manager, project string) ([]string, error) {
	units, err := m.List()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range service.Kinds {
		for _, u := range units {
			if u.Project == project && u.Kind == k {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

// runServiceCmd is `agentbus service …` (all but run).
func runServiceCmd(project string, args []string, d serviceCmdDeps, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	if d.Manager == nil {
		if sub == "list" || (sub == "remove" && len(rest) == 1 && rest[0] == "--all") {
			fmt.Fprintf(stdout, "no service manager (%s): no service installed\n", d.Why)
			return 0
		}
		fmt.Fprintf(stderr, "agentbus service: no service manager (%s); dispatch and missions run as herdr tabs (agentbus up)\n", d.Why)
		return 1
	}
	m := d.Manager
	switch sub {
	case "list":
		units, err := m.List()
		if err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		}
		if len(units) == 0 {
			fmt.Fprintf(stdout, "no agentbus service installed (%s)\n", m.Name())
		}
		for _, u := range units {
			st, _ := m.State(u.Project, u.Kind)
			fmt.Fprintf(stdout, "%-24s %-9s %s\n", u.Project, u.Kind, describeState(st))
		}
		return 0
	case "remove":
		if len(rest) == 1 && rest[0] == "--all" {
			units, err := m.List()
			if err != nil {
				fmt.Fprintln(stderr, "error: "+err.Error())
				return 1
			}
			code := 0
			cleared := map[string]bool{}
			for _, u := range units {
				if err := removeProjectService(m, d.Paths, u.Project, u.Kind); err != nil {
					fmt.Fprintf(stderr, "error: %s %s: %v\n", u.Project, u.Kind, err)
					code = 1
					continue
				}
				fmt.Fprintf(stdout, "removed %s %s\n", u.Project, u.Kind)
				// Its last unit gone, the project's services marker goes too (best effort:
				// the broker may be down during an uninstall).
				if left, err := projectKinds(m, u.Project); err == nil && len(left) == 0 && !cleared[u.Project] && bus.ValidName(u.Project) {
					cleared[u.Project] = true
					if b, err := d.Bus(u.Project); err == nil {
						_ = b.ClearServices(context.Background())
					}
				}
			}
			return code
		}
	}
	if !bus.ValidName(project) {
		fmt.Fprintln(stderr, "error: project required: agentbus --project <p> service "+sub)
		return 2
	}
	kinds, err := projectKinds(m, project)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	n := 50
	if sub == "logs" {
		for i := 0; i < len(rest); i++ {
			if rest[i] == "-n" && i+1 < len(rest) {
				v, err := strconv.Atoi(rest[i+1])
				if err != nil || v <= 0 {
					fmt.Fprintln(stderr, serviceUsage)
					return 2
				}
				n = v
				rest = append(rest[:i:i], rest[i+2:]...)
				i--
			}
		}
	}
	if len(rest) > 1 || (len(rest) == 1 && !service.ValidKind(rest[0])) {
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	if len(rest) == 1 {
		kinds = []string{rest[0]}
	}
	if len(kinds) == 0 {
		fmt.Fprintf(stdout, "no service installed for %s (agentbus up %s installs them)\n", project, project)
		return 0
	}
	code := 0
	fail := func(kind string, err error) {
		fmt.Fprintf(stderr, "error: %s: %v\n", kind, err)
		code = 1
	}
	switch sub {
	case "status":
		var recs map[string]bus.ServiceRecord
		if b, err := d.Bus(project); err == nil {
			_, recs, _ = b.ServiceStates(context.Background())
		} else {
			fmt.Fprintf(stdout, "(broker unreachable: %v)\n", err)
		}
		for _, k := range kinds {
			st, err := m.State(project, k)
			if err != nil {
				fail(k, err)
				continue
			}
			fmt.Fprintf(stdout, "%-9s %s\n", k, describeState(st))
			if r, ok := recs[k]; ok {
				fmt.Fprintf(stdout, "          %s\n", describeRecord(r, time.Now()))
			}
			fmt.Fprintf(stdout, "          log: %s\n", d.Paths.LogFile(project, k))
		}
	case "start":
		for _, k := range kinds {
			_ = service.Budget{Path: d.Paths.BudgetFile(project, k)}.Reset()
			if err := m.Start(project, k); err != nil {
				fail(k, err)
				continue
			}
			fmt.Fprintf(stdout, "%s: started\n", k)
		}
	case "stop", "restart":
		for _, k := range kinds {
			if err := m.Stop(project, k); err != nil {
				fail(k, err)
				continue
			}
			if sub == "restart" {
				_ = service.Budget{Path: d.Paths.BudgetFile(project, k)}.Reset()
				if err := m.Start(project, k); err != nil {
					fail(k, err)
					continue
				}
			}
			done := "stopped"
			if sub == "restart" {
				done = "restarted"
			}
			fmt.Fprintf(stdout, "%s: %s\n", k, done)
		}
	case "logs":
		for _, k := range kinds {
			// The two sources are read apart: a service that died before its log
			// existed has only the crash file.
			lines, err := service.TailLog(d.Paths.LogFile(project, k), n)
			crash, cerr := service.TailLog(d.Paths.CrashFile(project, k), 20)
			if err != nil && cerr != nil {
				fail(k, err)
				continue
			}
			if len(kinds) > 1 {
				fmt.Fprintf(stdout, "== %s (%s)\n", k, d.Paths.LogFile(project, k))
			}
			for _, l := range lines {
				fmt.Fprintln(stdout, l)
			}
			if cerr == nil && len(crash) > 0 {
				fmt.Fprintf(stdout, "== %s crash output (%s)\n", k, d.Paths.CrashFile(project, k))
				for _, l := range crash {
					fmt.Fprintln(stdout, l)
				}
			}
		}
	case "remove":
		for _, k := range kinds {
			if err := removeProjectService(m, d.Paths, project, k); err != nil {
				fail(k, err)
				continue
			}
			fmt.Fprintf(stdout, "%s: removed (data and logs kept)\n", k)
		}
		if left, err := projectKinds(m, project); err == nil && len(left) == 0 {
			_ = os.Remove(d.Paths.EnvFile(project))
			if b, err := d.Bus(project); err == nil {
				_ = b.ClearServices(context.Background())
			}
		}
	default:
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	return code
}

// removeProjectService stops and deletes one unit; the env file goes with the
// project's last unit (remove --all removes every one).
func removeProjectService(m service.Manager, p service.Paths, project, kind string) error {
	if err := m.Remove(project, kind); err != nil {
		return err
	}
	if left, err := projectKinds(m, project); err == nil && len(left) == 0 {
		if err := os.Remove(p.EnvFile(project)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func describeState(st service.State) string {
	s := st.Status
	if s == "" {
		s = "unknown"
	}
	if st.PID > 0 {
		s += fmt.Sprintf(", pid %d", st.PID)
	}
	return s
}

func describeRecord(r bus.ServiceRecord, now time.Time) string {
	age := now.Sub(time.UnixMilli(r.At)).Truncate(time.Second)
	s := fmt.Sprintf("%s %v ago", r.State, age)
	if r.Detail != "" {
		s += ": " + r.Detail
	}
	if r.Error != "" {
		s += " (last error: " + r.Error + ")"
	}
	if r.Host != "" {
		s += " on " + r.Host
	}
	return s
}

// runDown is `agentbus down <project>`: stop the project's services, delete
// nothing. The agents and the herdr workspace are left as they are.
func runDown(args []string, d serviceCmdDeps, stdout, stderr io.Writer) int {
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(stderr, "usage: agentbus down <project>")
		return 2
	}
	p := args[0]
	if d.Manager == nil {
		fmt.Fprintf(stdout, "no service manager (%s): dispatch and missions run as herdr tabs; close those tabs to stop them\n", d.Why)
		return 0
	}
	kinds, err := projectKinds(d.Manager, p)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	if len(kinds) == 0 {
		fmt.Fprintf(stdout, "no service installed for %s: nothing to stop\n", p)
		return 0
	}
	var recs map[string]bus.ServiceRecord
	if b, err := d.Bus(p); err == nil {
		_, recs, _ = b.ServiceStates(context.Background())
	}
	code := 0
	for _, k := range kinds {
		if err := d.Manager.Stop(p, k); err != nil {
			fmt.Fprintf(stderr, "error: %s: %v\n", k, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "%s: stopped\n", k)
		if r := recs[k]; k == service.Missions && r.State == "busy" {
			fmt.Fprintf(stdout, "  missions was %s: a check or integration in progress was interrupted (its tests killed with their process group); the conductor redoes it at the next start\n", r.Detail)
		}
	}
	fmt.Fprintf(stdout, "%s: services stopped, nothing deleted. The agents and the herdr workspace stay open (close them in herdr). agentbus up %s starts the services again.\n", p, p)
	return code
}

// mainServiceRun is `agentbus --project P service run <kind> [--stderr] …`,
// the command a unit runs: environment file first, then the broker.
func mainServiceRun(project, host string, args []string) int {
	if !bus.ValidName(project) || len(args) == 0 || !service.ValidKind(args[0]) {
		fmt.Fprintln(os.Stderr, "agentbus service run: invalid project or kind; not restarting")
		return 0
	}
	kind, rest := args[0], args[1:]
	envFile := ""
	if len(rest) > 1 && rest[0] == "--env" {
		envFile, rest = rest[1], rest[2:]
	}
	toStderr := false
	if len(rest) > 0 && rest[0] == "--stderr" {
		toStderr, rest = true, rest[1:]
	}
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentbus service run: "+err.Error())
		return 0
	}
	if envFile == "" {
		envFile = paths.EnvFile(project)
	}
	env, envErr := service.LoadEnvFile(envFile)
	for k, v := range env {
		os.Setenv(k, v)
	}
	if paths, err = service.PathsFrom(os.Getenv); err != nil {
		return 0
	}
	_ = service.RotateIfLarger(paths.CrashFile(project, kind), service.LogMax)
	// A panic goes to the crash file, which service logs and busmon read (and
	// which launchd also appends stderr to). Opened here, never rotated while
	// this process runs, so the descriptor never goes stale.
	if cf, err := os.OpenFile(paths.CrashFile(project, kind), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_ = debug.SetCrashOutput(cf, debug.CrashOptions{})
		cf.Close()
	}
	var w io.Writer = os.Stderr
	if lf, err := service.OpenLog(paths.LogFile(project, kind), service.LogMax, service.LogKeep); err == nil {
		defer lf.Close()
		w = lf
		if toStderr {
			w = io.MultiWriter(lf, os.Stderr)
		}
	}
	if envErr != nil {
		fmt.Fprintf(w, "service: invalid configuration: %v (agentbus up %s rewrites it); not restarting\n", envErr, project)
		return 0
	}
	// A spent budget gives up before anything else can fail (launchd has no
	// start limit: this is what bounds a broker that stays down).
	if budget := (service.Budget{Path: paths.BudgetFile(project, kind)}); budget.Spent(time.Now()) {
		fmt.Fprintf(w, "service: gave up: %d failed starts in %v; agentbus up %s tries again\n", service.BudgetStarts, service.BudgetWindow, project)
		return 0
	}
	if u := os.Getenv("REDIS_URL"); u != "" {
		if _, err := redis.ParseURL(u); err != nil {
			fmt.Fprintf(w, "service: invalid configuration: REDIS_URL is not a valid Redis URL (not shown: it may hold the password); not restarting\n")
			return 0
		}
	}
	client, err := bus.Connect(host)
	if err != nil {
		budget := service.Budget{Path: paths.BudgetFile(project, kind)}
		_ = budget.Failed(time.Now())
		if budget.Spent(time.Now()) { // systemd would refuse the next start: give up now
			fmt.Fprintf(w, "service: broker unreachable: %v; gave up after %d failed starts in %v (agentbus up %s tries again)\n", err, service.BudgetStarts, service.BudgetWindow, project)
			return 0
		}
		fmt.Fprintf(w, "service: broker unreachable: %v; the manager retries\n", err)
		return 1
	}
	b, err := bus.Open(client, project)
	if err != nil {
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := dispatch.Herdr{Bin: os.Getenv("HERDR_BIN")}
	hostname, _ := os.Hostname()
	deps := serviceRunDeps{
		Paths: paths, Host: hostname, Heartbeat: 5 * time.Second, Standby: 15 * time.Second, HerdrPing: h.Ping, Now: time.Now,
		RunDispatch: func(ctx context.Context, b *bus.Bus, escalateTo string, onLease func(bool), log io.Writer) int {
			cfg := dispatchBaseConfig()
			cfg.Log, cfg.OnLease = log, onLease
			cfg.Quota = serviceQuota(os.Getenv, log)
			cfg.Observe = dispatch.ObserveEvery
			cfg.Health = realHealthConfig(b, h, paths)
			if escalateTo != "" {
				cfg.EscalateTo = escalateTo
			}
			return superviseDispatch(ctx, b, h, cfg, log)
		},
		RunMissions: mission.Run,
	}
	return serviceRun(ctx, b, kind, rest, deps, w)
}
