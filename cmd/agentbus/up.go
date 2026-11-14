package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/trust"
)

// upDeps is what `agentbus up` touches, injected for tests.
type upDeps struct {
	Herdr func(args ...string) ([]byte, error) // the herdr CLI
	// IgnoreWorkspace is never taken for the project's open workspace: the
	// first one herdr made for a session agentbus start launched (start-tabs).
	IgnoreWorkspace string
	Bootstrap       func(args []string) error // scripts/bootstrap <args>
	Env             func(string) string

	// The project services (v0.12). Services finds the manager, or says why
	// there is none; nil means none.
	Services func() (service.Manager, string)
	// AtLogin is the machine's services.start_at_login (self-repair step 9);
	// nil reads as off.
	AtLogin func() bool
	Paths   service.Paths
	Exe     func() (string, error) // this agentbus: the units run it when Installed is absent
	// Installed is the agentbus setup.sh installed (buildinfo.Bin), "" when
	// unknown: when it exists the units run it, whoever runs up.
	Installed string
	Environ   []string // the service environment is picked from it
	// The holder (herdr exit, spec 4.4a): HolderBuild is the raw build id of
	// the binary Exe returns (holder.BuildID), nil for no holder; HolderAnswers
	// says whether a holder socket of the project's build key answers (the gc
	// keeps that build).
	HolderBuild   func() string
	HolderAnswers func(project, key string) bool
	// KeepEnv keeps the project's env file as it is, its herdr session
	// excepted: a suspended project's resume never changes its configuration
	// (suspend 3a). What Environ would have changed is named, never its value.
	KeepEnv bool
	Mark    func(project string, inst bus.ServicesInstall) error
	Host    string
	// Ping asks the broker for PING, within timeout, before the marker is written
	// (up-broker-ready): a broker docker has just started answers LOADING for a while.
	// Now is the clock of that wait (nil: time.Now). Nil Ping: no wait.
	Ping func(timeout time.Duration) error
	Now  func() time.Time

	// The relaunch of an open workspace (v0.21): the agents' snapshots, the
	// process start time (nil off Linux: snapshots then decide nothing) and the
	// per-project lock that keeps two ups from typing into one pane.
	Agents func(project string) (map[string]bus.AgentSnapshot, error)
	// Intent is a role's swap intent, if held (self-repair spec 4.1): up
	// skips a role a self-repair, a reload, a swap or a retire relaunches.
	Intent    func(project, role string) (bus.SwapIntent, bool, error)
	ProcStart func(int) (uint64, error)
	Lock      func(project string, wait bool) (unlock func(), err error)

	// The trust gate (specialist roles spec, part B; trust.go). TabClient is
	// the client a tab's command launches ("" for none); Trusted reads that
	// client's trust of the project folder. Nil Trusted: no gate.
	TabClient func(command string) string
	Trusted   func(client, dir string) bool
	// TrustAsks is the project's asks on the broker, for the trust reminder
	// in busmon (first-run-trust (b)); nil: no reminder, the line only.
	TrustAsks    func(project string) (trustAskBus, error)
	Sleep        func(time.Duration)
	TrustPoll    time.Duration
	TrustTimeout time.Duration
	Out          io.Writer // the gate's messages (runUp's stdout)

	// The agents' CLIs (installer-no-client): TabExe is the program a tab's
	// agent runs ("" for none), LookPath finds it on up's PATH. Nil: no check.
	TabExe   func(command string) string
	LookPath func(string) (string, error)

	// The workspace hook on a suspended project (suspend spec §8). Nil
	// SuspendPause: the hook reads no pause and starts the services as before.
	SuspendPause func(project string) (*bus.PauseRecord, error) // bounded, 2 s
	AutoResume   func() bool                                    // suspend.auto_resume
	Boot         func() string                                  // this host's boot, "" unknown
	Hostname     func() (string, error)
	Person       func() string // identity.as
	// SpawnResume starts agentbus --project <p> resume --yes detached, its
	// output in log, and does not wait.
	SpawnResume func(project, log string) error
}

// upTab and upTemplate are the project template's tab and the template
// itself, as the relaunch core reads them.
type (
	upTab      = relaunch.Tab
	upTemplate = relaunch.Template
)

// runUp provisions a project (bootstrap, idempotent) and, inside herdr, opens
// its workspace directly — busmon first and focused, then every tab of the
// template — or focuses it when it is already open. It replaces "bootstrap new,
// then Ctrl+B Up and pick the project". Outside herdr it provisions and says how
// to open it.
func runUp(args []string, d upDeps, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: agentbus up <project> [<path>] [--as <your-name>] [--tabs] [--resume] [--dry-run] [--escalate-to <agent>] [--reload on|propose|off] [bootstrap flags: --no-dispatch --no-missions --session <s> --index --cron --yes]")
		return 1
	}
	if !bus.ValidName(args[0]) { // stranger-test: say why, and what would do
		fmt.Fprintf(stderr, "agentbus up: invalid project name %q: a lowercase letter, then lowercase letters, digits, - or _, up to 32 in all\n", args[0])
		if low := strings.ToLower(args[0]); bus.ValidName(low) {
			try := []string{"agentbus", "up", low}
			for _, a := range args[1:] {
				try = append(try, shellWord(a))
			}
			fmt.Fprintf(stderr, "try: %s\n", strings.Join(try, " "))
		}
		return 1
	}
	project := args[0]
	args, tabs := extractBool(args, "--tabs")
	args, resume := extractBool(args, "--resume")
	args, dryRun := extractBool(args, "--dry-run")
	// The person's name recorded at install is the default --as (v0.21).
	if !slices.Contains(args, "--as") {
		if as := settingsIdentity(d.Env); as != "" {
			args = append(args, "--as", as)
		}
	}
	if dryRun {
		return upDryRun(d, project, resume, stdout, stderr)
	}
	if d.Lock != nil {
		unlock, err := d.Lock(project, true)
		if err != nil {
			fmt.Fprintf(stderr, "agentbus up: %v; nothing done\n", err)
			return 1
		}
		defer unlock()
	}
	args, escalate := extractFlag(args, "--escalate-to")
	if escalate != "" && !bus.ValidName(escalate) {
		fmt.Fprintf(stderr, "agentbus up: --escalate-to: invalid agent name %q\n", escalate)
		return 1
	}
	args, reload := extractFlag(args, "--reload")
	if reload != "" && reload != "on" && reload != "propose" && reload != "off" {
		fmt.Fprintf(stderr, "agentbus up: --reload: want on, propose or off, not %q\n", reload)
		return 1
	}
	kinds := []string{}
	if !slices.Contains(args, "--no-dispatch") {
		kinds = append(kinds, service.Dispatch)
	}
	if !slices.Contains(args, "--no-missions") {
		kinds = append(kinds, service.Missions)
	}
	// Service mode unless --tabs or no manager: then the template holds no
	// dispatch or missions tab, and the services run whatever herdr does.
	var m service.Manager
	why := "--tabs"
	if !tabs && d.Services != nil {
		m, why = d.Services()
	} else if !tabs {
		why = "no service manager configured"
	}
	if m != nil && herdrSession(args, d.Environ) == "" {
		fmt.Fprintln(stderr, "agentbus up: cannot tell the herdr session: HERDR_SESSION is unset and HERDR_SOCKET_PATH is not <config>/sessions/<name>/herdr.sock; pass --session <name>")
		return 1
	}
	bootArgs := args
	if m != nil {
		bootArgs = append(slices.Clone(args), "--no-dispatch", "--no-missions")
	}
	if err := d.Bootstrap(bootArgs); err != nil {
		fmt.Fprintf(stderr, "agentbus up: bootstrap failed: %v\n", err)
		return 1
	}
	path := templatePath(d, project)
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: reading %s: %v\n", path, err)
		return 1
	}
	if !checkProfiles(d, tpl, path, stderr) {
		return 1
	}
	absent, ok := checkClients(d, project, tpl, stdout, stderr)
	if !ok {
		return 1
	}
	if len(absent) > 0 && d.TabClient != nil { // no trust gate for a CLI that is not there (foureyes)
		clientOf := d.TabClient
		d.TabClient = func(cmd string) string {
			if absent[d.TabExe(cmd)] {
				return ""
			}
			return clientOf(cmd)
		}
	}
	failed := false
	if d.AtLogin != nil && d.AtLogin() {
		if _, why := service.LoginStartOf(m); why != "" {
			fmt.Fprintf(stderr, "agentbus up: services.start_at_login is on, but %s; the services start with agentbus up only\n", why)
		}
	}
	if m != nil {
		if err := startServices(d, m, project, tpl.WorkingDir, herdrSession(args, d.Environ), escalate, reload, kinds, stdout); err != nil {
			fmt.Fprintf(stderr, "agentbus up: services: %v\n", err)
			if errors.Is(err, errNotInstalled) {
				return 1
			}
			failed = true
		}
		tpl.Tabs = slices.DeleteFunc(tpl.Tabs, isServiceTab)
	} else {
		if !tabs {
			fmt.Fprintf(stdout, "no service manager (%s): dispatch and missions run as herdr tabs, and stop when the workspace closes\n", why)
		}
		if reload != "" {
			fmt.Fprintf(stderr, "agentbus up: --reload is recorded for services only; in tab mode export AGENT_BUS_RELOAD in sentinel's environment\n")
		}
		if escalate != "" {
			fmt.Fprintf(stderr, "agentbus up: --escalate-to applies to services only; the tabs escalate to master\n")
		}
		// A template written in service mode lacks the tabs: write it again.
		if missingServiceTab(tpl, kinds) {
			// Rewrite it in place: without a path bootstrap would take $PWD, so
			// the project keeps the directory its template already names.
			newArgs := append([]string{"new"}, args...)
			if (len(args) < 2 || strings.HasPrefix(args[1], "--")) && tpl.WorkingDir != "" {
				newArgs = append([]string{"new", project, tpl.WorkingDir}, args[1:]...)
			}
			if err := d.Bootstrap(newArgs); err != nil {
				fmt.Fprintf(stderr, "agentbus up: bootstrap failed: %v\n", err)
				return 1
			}
			tpl = upTemplate{}
			if _, err := toml.DecodeFile(path, &tpl); err != nil {
				fmt.Fprintf(stderr, "agentbus up: reading %s: %v\n", path, err)
				return 1
			}
		}
	}
	code := 0
	if failed {
		code = 1
	}
	if d.Env("HERDR_ENV") != "1" {
		sess := herdrSession(args, d.Environ)
		if sess == "" {
			sess = project // an unreadable socket path names none: the hint falls back to the project
		}
		fmt.Fprintf(stdout, "provisioned %s. To open it, start herdr (herdr --session %s), then run: agentbus up %s\n", project, sess, project)
		return code
	}
	if id, err := openWorkspace(d, project); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	} else if id != "" {
		fmt.Fprintf(stdout, "%s is already open: focused workspace %s; checking its tabs\n", project, id)
		if err := relaunchUp(d, project, id, tpl, relaunch.Options{Resume: resume, Create: true}, stdout); err != nil {
			fmt.Fprintf(stderr, "agentbus up: %v\n", err)
			code = 1
		}
		return code
	}
	if len(tpl.Tabs) == 0 {
		fmt.Fprintf(stderr, "agentbus up: %s has no tabs\n", path)
		return 1
	}
	if d.Out == nil {
		d.Out = stdout
	}
	if err := layout(d, project, tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "opened %s: %d tabs, busmon first (reopen later with Ctrl+B then Up)\n", project, len(tpl.Tabs))
	return code
}

// herdrSession is the session the project opens in, as bootstrap resolves
// it: --session, else the caller's HERDR_SESSION, else the session named by
// the caller's HERDR_SOCKET_PATH, else the project name. A socket path that
// names no session gives "": the services must not guess. Bootstrap does not
// read the socket: it takes --session, HERDR_SESSION, then the project name.
func herdrSession(args, environ []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session" {
			return args[i+1]
		}
	}
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "HERDR_SESSION="); ok && v != "" {
			return v
		}
	}
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "HERDR_SOCKET_PATH="); ok && v != "" {
			name, _ := sessionFromSocket(v)
			return name
		}
	}
	return args[0]
}

// isServiceTab is a template tab that runs dispatch or missions, which a
// service runs instead (an older template still has them).
func isServiceTab(t upTab) bool {
	cmd := strings.TrimSpace(t.Command)
	return t.Name == service.Dispatch || t.Name == service.Missions ||
		strings.HasSuffix(cmd, " dispatch") || strings.Contains(cmd, " missions --repo")
}

func missingServiceTab(tpl upTemplate, kinds []string) bool {
	for _, k := range kinds {
		if !slices.ContainsFunc(tpl.Tabs, func(t upTab) bool { return t.Name == k }) {
			return true
		}
	}
	return false
}

// keptEnv is the project's env file to keep, its herdr keys replaced by the
// caller's (the session's socket may move with a new herdr server). It names
// on out every other key the caller's environment sets differently, or not at
// all, never a value. No file: nil, nothing to keep. A file it cannot read is
// an error: a configuration never read is never replaced.
func keptEnv(path string, caller map[string]string, out io.Writer) (map[string]string, error) {
	old, err := service.LoadEnvFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the project's settings cannot be read to keep them (%v): fix the file, or run agentbus up to write it anew", err)
	}
	var differ []string
	for _, k := range slices.Sorted(maps.Keys(caller)) {
		if strings.HasPrefix(k, "HERDR_") {
			continue
		}
		if v, ok := old[k]; !ok || v != caller[k] {
			differ = append(differ, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(old)) {
		if _, ok := caller[k]; !ok && !strings.HasPrefix(k, "HERDR_") && k != service.EscalateEnv && k != service.ReloadEnv {
			differ = append(differ, k)
		}
	}
	for _, k := range []string{"HERDR_SESSION", "HERDR_SOCKET_PATH"} {
		if v, ok := caller[k]; ok {
			old[k] = v
		}
	}
	if len(differ) > 0 {
		slices.Sort(differ)
		fmt.Fprintf(out, "services: the project's settings are kept; your environment differs on %s (agentbus up applies yours)\n", strings.Join(slices.Compact(differ), ", "))
	}
	return old, nil
}

// errNotInstalled: up refused to write a unit at all.
var errNotInstalled = errors.New("services not installed")

// startServices writes the environment file, the marker and the units, then
// starts each service that is not already running with this configuration.
// escalate, when set, becomes the project's escalation target; when not, the
// one an earlier up recorded is kept, so a plain re-run never resets it.
func startServices(d upDeps, m service.Manager, project, workdir, session, escalate, reload string, kinds []string, stdout io.Writer) error {
	exe, err := d.Exe()
	if err != nil {
		return fmt.Errorf("%w: cannot find this agentbus: %v", errNotInstalled, err)
	}
	if d.Installed != "" {
		if r, err := filepath.EvalSymlinks(d.Installed); err == nil {
			if st, err := os.Stat(r); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
				exe = r
			}
		}
	}
	if strings.Contains(exe, "go-build") || !filepath.IsAbs(exe) {
		return fmt.Errorf("%w: a unit must run an installed agentbus, not %s (./setup.sh install, then run that one)", errNotInstalled, exe)
	}
	env := service.CollectEnv(d.Environ)
	if d.KeepEnv {
		kept, err := keptEnv(d.Paths.EnvFile(project), env, stdout)
		if err != nil {
			return fmt.Errorf("%w: %v", errNotInstalled, err)
		}
		if kept != nil {
			env = kept
		}
	}
	if env["HERDR_SESSION"] != session { // another session's socket would reach the wrong server
		if name, _ := sessionFromSocket(env["HERDR_SOCKET_PATH"]); name != session {
			delete(env, "HERDR_SOCKET_PATH")
		}
	}
	env["HERDR_SESSION"] = session
	if escalate != "" {
		env[service.EscalateEnv] = escalate
	} else if _, set := env[service.EscalateEnv]; !set {
		if old, err := service.LoadEnvFile(d.Paths.EnvFile(project)); err == nil && bus.ValidName(old[service.EscalateEnv]) {
			env[service.EscalateEnv] = old[service.EscalateEnv]
		}
	}
	switch reload { // like the escalation target: kept unless given
	case "on":
		delete(env, service.ReloadEnv)
	case "propose", "off":
		env[service.ReloadEnv] = reload
	default:
		if _, set := env[service.ReloadEnv]; !set {
			if old, err := service.LoadEnvFile(d.Paths.EnvFile(project)); err == nil && (old[service.ReloadEnv] == "propose" || old[service.ReloadEnv] == "off") {
				env[service.ReloadEnv] = old[service.ReloadEnv]
			}
		}
	}
	// The holder's launch context, read by the holder alone at each Start (it
	// is never restarted for a new one; herdr exit 6b2): the checkout, the
	// manifest this up's template names, the working directory.
	holderContext(d, workdir, env)
	envChanged, err := service.WriteEnvFile(d.Paths.EnvFile(project), env)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotInstalled, err)
	}
	if err := waitBroker(d.Ping, d.Sleep, d.Now); err != nil {
		return fmt.Errorf("%w: marking %s: the broker did not answer: %v", errNotInstalled, bus.ServicesKey(project), err)
	}
	if err := d.Mark(project, bus.ServicesInstall{Kinds: kinds, Manager: m.Name(), Host: d.Host}); err != nil {
		return fmt.Errorf("%w: marking %s: %v", errNotInstalled, bus.ServicesKey(project), err)
	}
	var errs []error
	// Start at login (self-repair step 9): off, a project that was enabled is
	// disabled before its units lose their [Install] section (systemd
	// disables by that section); on, the units are written for it and enabled
	// once installed.
	atLogin := d.AtLogin != nil && d.AtLogin()
	ls, _ := service.LoginStartOf(m)
	// A state that cannot be read is disabled anyway; a disable that fails
	// stops here, before any unit loses the section (reviewer-codex).
	if ls != nil && !atLogin {
		if on, err := ls.Enabled(project); on || err != nil {
			if derr := ls.Disable(project); derr != nil {
				return fmt.Errorf("%w: start at login is on or unknown (%v) and could not be disabled: %v; no unit was rewritten",
					errNotInstalled, err, derr)
			}
			if on {
				fmt.Fprintf(stdout, "services: no longer start at login (services.start_at_login is off)\n")
			}
		}
	}
	// The holder before the services: it makes no broker connection, and the
	// dispatcher's first health pass must find it answering, or holder fails
	// for agents that were never lost (foureyes).
	if err := startHolder(d, m, project, workdir, stdout); err != nil {
		errs = append(errs, fmt.Errorf("holder: %v", err))
	}
	for _, k := range kinds {
		spec := service.Spec{Project: project, Kind: k, Exe: exe, WorkDir: workdir, Paths: d.Paths, EnvFile: d.Paths.EnvFile(project),
			AtLogin: atLogin && ls != nil}
		if k == service.Missions {
			spec.Args = []string{"--repo", workdir}
		}
		changed, err := m.Install(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %v", k, err))
			continue
		}
		_ = service.Budget{Path: d.Paths.BudgetFile(project, k)}.Reset()
		st, _ := m.State(project, k)
		reconfigure := changed || envChanged
		verb := "started"
		switch {
		case st.Active && !reconfigure:
			fmt.Fprintf(stdout, "%s: already running (%s)\n", k, m.Name())
			continue
		case st.Active && k == service.Missions:
			// Never stop a running conductor for a new configuration: its
			// published state can be seconds old, and a mission may have just
			// started an integration.
			fmt.Fprintf(stdout, "%s: running, left as is; its new configuration applies at its next restart (agentbus --project %s service restart %s when no change is integrating)\n", k, project, k)
			continue
		case reconfigure:
			// A loaded but stopped launchd job keeps its old definition until it
			// is booted out: reload before the start, running or not.
			if err := m.Stop(project, k); err != nil {
				errs = append(errs, fmt.Errorf("%s: %v", k, err))
				continue
			}
			if st.Active {
				verb = "restarted (its configuration changed)"
			}
		}
		if err := m.Start(project, k); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v (agentbus --project %s service logs %s)", k, err, project, k))
			continue
		}
		fmt.Fprintf(stdout, "%s: %s as a %s service; logs: agentbus --project %s service logs %s\n", k, verb, m.Name(), project, k)
	}
	for _, k := range service.Kinds {
		if slices.Contains(kinds, k) {
			continue
		}
		if st, err := m.State(project, k); err == nil && st.Active {
			fmt.Fprintf(stdout, "%s: still running from before (stop it: agentbus --project %s service stop %s)\n", k, project, k)
		}
	}
	if ls != nil && atLogin {
		if err := ls.Enable(project); err != nil {
			errs = append(errs, fmt.Errorf("start at login: %v", err))
		} else {
			fmt.Fprintf(stdout, "services: start at login (services.start_at_login; no linger: only once you log in)\n")
		}
	}
	return errors.Join(errs...)
}

// openWorkspace focuses an already open workspace labelled project and returns
// its id, or "" when there is none.
func openWorkspace(d upDeps, project string) (string, error) {
	id, err := findWorkspace(d, project)
	if err != nil || id == "" {
		return id, err
	}
	if _, err := d.Herdr("workspace", "focus", id); err != nil {
		return "", fmt.Errorf("herdr workspace focus: %w", err)
	}
	return id, nil
}

// findWorkspace is the id of the open workspace labelled project, or "".
func findWorkspace(d upDeps, project string) (string, error) {
	return relaunch.FindWorkspace(d.Herdr, project, d.IgnoreWorkspace)
}

// layout opens the workspace the way herdr-plus does: the first tab reuses the
// focused root tab, the others are created behind it, and each tab's command
// runs once every pane exists.
func layout(d upDeps, project string, tpl upTemplate) error {
	dir := os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1))
	var created struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
			Tab struct {
				TabID string `json:"tab_id"`
			} `json:"tab"`
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	out, err := d.Herdr("workspace", "create", "--cwd", dir, "--label", project, "--focus")
	if err != nil {
		return fmt.Errorf("herdr workspace create: %w", err)
	}
	if err := json.Unmarshal(out, &created); err != nil || created.Result.Workspace.WorkspaceID == "" {
		return fmt.Errorf("herdr workspace create: unexpected reply %s", strings.TrimSpace(string(out)))
	}
	ws, panes := created.Result.Workspace.WorkspaceID, []string{created.Result.RootPane.PaneID}
	if err := fill(d, ws, created.Result.Tab.TabID, panes, tpl); errors.Is(err, errTrustPending) {
		return err // the started agents stay; up again starts the others
	} else if err != nil {
		// A half-made workspace would be focused by the next "agentbus up" as if it were open.
		if _, cerr := d.Herdr("workspace", "close", ws); cerr != nil {
			return fmt.Errorf("%w (and closing the half-made workspace %s failed: %v; close it by hand)", err, ws, cerr)
		}
		return fmt.Errorf("%w (closed the half-made workspace %s; run agentbus up again)", err, ws)
	}
	return nil
}

// fill names the root tab, creates the others and runs each tab's command.
func fill(d upDeps, ws, rootTab string, panes []string, tpl upTemplate) error {
	if _, err := d.Herdr("tab", "rename", rootTab, tpl.Tabs[0].Name); err != nil {
		return fmt.Errorf("herdr tab rename: %w", err)
	}
	for _, t := range tpl.Tabs[1:] {
		out, err := d.Herdr("tab", "create", "--workspace", ws, "--label", t.Name, "--no-focus")
		if err != nil {
			return fmt.Errorf("herdr tab create %s: %w", t.Name, err)
		}
		var tab struct {
			Result struct {
				RootPane struct {
					PaneID string `json:"pane_id"`
				} `json:"root_pane"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &tab); err != nil || tab.Result.RootPane.PaneID == "" {
			return fmt.Errorf("herdr tab create %s: unexpected reply %s", t.Name, strings.TrimSpace(string(out)))
		}
		panes = append(panes, tab.Result.RootPane.PaneID)
	}
	run := func(idx []int) error {
		for _, i := range idx {
			if _, err := d.Herdr("pane", "run", panes[i], tpl.Tabs[i].Command); err != nil {
				return fmt.Errorf("herdr pane run (%s): %w", tpl.Tabs[i].Name, err)
			}
		}
		return nil
	}
	dir := os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1))
	now, later, gates := trustPlan(tpl.Tabs, d.TabClient, func(client string) bool {
		return d.Trusted == nil || d.Trusted(client, dir)
	})
	if err := run(now); err != nil {
		return err
	}
	paneOf := map[string]string{} // A10: the gate names the pane to answer in
	for i, t := range tpl.Tabs {
		paneOf[t.Name] = panes[i]
	}
	settle := d.trustReminders(tpl, dir, gates, paneOf) // the ask in busmon, also when no agent waits
	if len(later) == 0 {
		return nil
	}
	return d.waitTrust(tpl, dir, gates, paneOf, func() error {
		settle() // accepted: the reminder closes itself
		return run(later)
	})
}

// holderContext records the holder's launch context in env (service.Holder*
// names); a value up cannot tell is left out, and the holder falls back.
func holderContext(d upDeps, workdir string, env map[string]string) {
	set := func(k, v string) {
		if filepath.IsAbs(v) {
			env[k] = v
		} else {
			delete(env, k)
		}
	}
	set(service.HolderDirEnv, expandWorkDir(d, workdir))
	repo, _ := repoDir(d.Env)
	set(service.HolderRepoEnv, repo)
	manifest, _ := rolecfg.ManifestPath(d.Env)
	set(service.HolderManifestEnv, manifest)
}

// The readiness wait: one deadline for the whole wait, probes 100 ms apart;
// each real probe is the holder's handshake (holderReady) bounded by
// holderProbeTimeout, so a socket that
// accepts and stalls cannot stretch it (reviewer-codex).
const (
	holderReadyWithin  = 5 * time.Second
	holderReadyEvery   = 100 * time.Millisecond
	holderProbeTimeout = 500 * time.Millisecond
)

// waitHolder probes until the holder answers or the deadline passes, on now's
// clock (nil: the real one).
func waitHolder(ready func() bool, sleep func(time.Duration), now func() time.Time) bool {
	if sleep == nil {
		sleep = time.Sleep
	}
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(holderReadyWithin)
	for {
		if ready() {
			return true
		}
		if !now().Add(holderReadyEvery).Before(deadline) {
			return false
		}
		sleep(holderReadyEvery)
	}
}

// expandWorkDir is a template's working_dir as up reads it elsewhere: ~ is
// the home, variables expanded.
func expandWorkDir(d upDeps, dir string) string {
	return os.ExpandEnv(strings.Replace(dir, "~", d.Env("HOME"), 1))
}

// startHolder installs this build's holder (spec 4.4a): its binary copy, its
// unit with the project's env file and working directory, then starts it and
// gcs the project's older builds that no longer run. It never stops or
// restarts a holder: an older build's agents live on in theirs. A manager
// without holder units, or no build id, installs none.
func startHolder(d upDeps, m service.Manager, project, workdir string, stdout io.Writer) error {
	hm, ok := m.(service.HolderManager)
	if !ok || d.HolderBuild == nil {
		return nil
	}
	raw := d.HolderBuild()
	self, err := d.Exe()
	if err != nil {
		return fmt.Errorf("cannot find this agentbus: %v", err)
	}
	if strings.Contains(self, "go-build") || !filepath.IsAbs(self) {
		// A developer's go run: the services run the installed agentbus, and no
		// holder is installed from a temporary build (foureyes: not an error).
		fmt.Fprintf(stdout, "holder: not installed from %s (a go run build); run the installed agentbus to install it\n", self)
		return nil
	}
	if _, err := service.InstallHolderBinary(d.Paths, raw, self); err != nil {
		return err
	}
	if _, err := hm.InstallHolder(service.HolderSpec{Project: project, Raw: raw, Paths: d.Paths,
		EnvFile: d.Paths.EnvFile(project), WorkDir: expandWorkDir(d, workdir)}); err != nil {
		return err
	}
	key := service.BuildKey(raw)
	if err := hm.StartHolder(project, key); err != nil {
		return err
	}
	answers := func(k string) bool { return d.HolderAnswers != nil && d.HolderAnswers(project, k) }
	// Ready, not only started (reviewer-codex): a manager's start returns
	// before the socket listens, and the dispatcher started next must find it
	// answering. Bounded: a holder that never answers is reported by the
	// holder check, and the services start anyway.
	if !waitHolder(func() bool { return answers(key) }, d.Sleep, d.Now) {
		fmt.Fprintf(stdout, "holder: build %s started as a %s service, not answering yet (agentbus health holder)\n", raw, m.Name())
	} else {
		fmt.Fprintf(stdout, "holder: build %s started as a %s service (no agent runs on it yet)\n", raw, m.Name())
	}
	removed, err := service.GCHolders(hm, d.Paths, project, key, answers)
	if len(removed) > 0 {
		fmt.Fprintf(stdout, "holder: removed %d older build(s) that no longer run\n", len(removed))
	}
	return err
}

// templatePath is where bootstrap writes the project's template
// (rolecfg.ProjectsDir, which scripts/bootstrap reads through roles projects-dir).
func templatePath(d upDeps, project string) string {
	return filepath.Join(rolecfg.ProjectsDir(d.Env), project+".toml")
}

// repoDir is the agent-bus-monitor checkout that holds scripts/bootstrap:
// AGENTBUS_REPO, else the one setup.sh stamped in the binary, else this
// binary's directory (the repository's own build).
func repoDir(env func(string) string) (string, error) {
	candidates := []string{env("AGENTBUS_REPO"), buildinfo.Repo}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "scripts", "bootstrap")); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("cannot find the agent-bus-monitor checkout (scripts/bootstrap): set AGENTBUS_REPO or reinstall with ./setup.sh install")
}

// realUpDeps runs the real herdr and the repository's bootstrap.
func realUpDeps(stdout, stderr io.Writer) upDeps { return realUpDepsIn(os.Stdin, stdout, stderr) }

// realUpDepsIn is realUpDeps with bootstrap's stdin given: start's run inside
// herdr gives none, herdr owning the terminal (foureyes).
func realUpDepsIn(stdin io.Reader, stdout, stderr io.Writer) upDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	host, _ := os.Hostname()
	return upDeps{
		Herdr: func(args ...string) ([]byte, error) {
			cmd := exec.Command(herdrBin(os.Getenv), args...)
			cmd.Stderr = stderr
			return cmd.Output()
		},
		Bootstrap: func(args []string) error {
			repo, err := repoDir(os.Getenv)
			if err != nil {
				return err
			}
			cmd := bootstrapCommand(repo, args)
			cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, stdin
			return cmd.Run()
		},
		Env: os.Getenv,
		Services: func() (service.Manager, string) {
			return service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		},
		AtLogin: func() bool { return machineAtLogin(os.Getenv) },
		Paths:   paths,
		Exe: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(exe)
		},
		Installed:     buildinfo.Bin,
		Environ:       os.Environ(),
		HolderBuild:   holder.BuildID,
		HolderAnswers: func(project, key string) bool { return holderReady(os.Getenv, project, key, holderProbeTimeout) },
		Mark: func(project string, inst bus.ServicesInstall) error {
			c, err := bus.Connect("")
			if err != nil {
				return err
			}
			defer c.Close()
			b, err := bus.Open(c, project)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return b.MarkServicesInstalled(ctx, inst)
		},
		Host: host,
		Ping: func(timeout time.Duration) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			c, err := bus.ConnectContext(ctx, "") // its own PING is the answer
			if err != nil {
				return err
			}
			return c.Close()
		},
		Agents: func(project string) (map[string]bus.AgentSnapshot, error) {
			return readAgents(project)
		},
		Intent:    readIntent,
		ProcStart: procStartFn(),
		Lock:      upLock(upCacheDir(os.Getenv)),
		TabClient: func(cmd string) string { return tabClient(cmd, os.Getenv) },
		TabExe:    func(cmd string) string { return tabExecutable(cmd, os.Getenv) },
		LookPath:  lookAgentCLI(os.Getenv, exec.LookPath, shellProbe(os.Getenv, 3*time.Second)),
		Trusted:   trust.Reader(os.Getenv),
		TrustAsks: func(project string) (trustAskBus, error) {
			c, err := bus.Connect("")
			if err != nil {
				return nil, err
			}
			return bus.Open(c, project) // the process ends with up: the connection with it
		},
		Sleep: time.Sleep,
		Out:   stdout,
	}
}

// relaunchUp reads the agents' snapshots (without them, the panes decide) and
// relaunches what is gone in the open workspace ws.
func relaunchUp(d upDeps, project, ws string, tpl upTemplate, o relaunch.Options, stdout io.Writer) error {
	var snaps map[string]bus.AgentSnapshot
	ok := false
	if d.Agents != nil {
		s, err := d.Agents(project)
		if err != nil {
			fmt.Fprintf(stdout, "agents unreadable (%v): the panes decide\n", err)
		} else {
			snaps, ok = s, true
		}
	}
	o.Dir = os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1))
	if d.Intent != nil {
		o.Held = func(role string) (string, bool) {
			in, held, err := d.Intent(project, role)
			if err != nil { // as for the agents: said, and the panes decide
				fmt.Fprintf(stdout, "%s: intent unreadable (%v): not checked\n", role, err)
				return "", false
			}
			return in.Kind, held
		}
	}
	// busmon's old shell needs herdr only, not the agents (start-busmon-pane).
	o.CloseBusmon = true
	if ok {
		// A relaunched agent is proven by its own bind before its old pane closes.
		sleep := d.Sleep
		if sleep == nil {
			sleep = time.Sleep
		}
		o.Prove = &relaunch.Proof{Agents: func() (map[string]bus.AgentSnapshot, error) { return d.Agents(project) }, Sleep: sleep, Tries: 60}
	}
	return relaunch.Open(d.Herdr, snaps, ok, d.ProcStart, project, ws, tpl, o, stdout)
}

// upDryRun prints what up would do to an open workspace, and changes nothing:
// no bootstrap, no service, no typing.
func upDryRun(d upDeps, project string, resume bool, stdout, stderr io.Writer) int {
	path := templatePath(d, project)
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: reading %s: %v\n", path, err)
		return 1
	}
	if d.Env("HERDR_ENV") != "1" {
		fmt.Fprintf(stdout, "dry run: outside herdr, up would provision %s and say how to open it\n", project)
		return 0
	}
	id, err := findWorkspace(d, project)
	if err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	}
	if id == "" {
		fmt.Fprintf(stdout, "dry run: %s is not open; up would open it with %d tabs\n", project, len(tpl.Tabs))
		dir := os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1))
		_, _, gates := trustPlan(tpl.Tabs, d.TabClient, func(client string) bool {
			return d.Trusted == nil || d.Trusted(client, dir)
		})
		key, _ := trust.Key(dir)
		for _, c := range slices.Sorted(maps.Keys(gates)) {
			fmt.Fprintf(stdout, "dry run: trust: %s not yet accepted for %s; %s first, the others after\n", trust.Label(c), key, gates[c])
		}
		return 0
	}
	tpl.Tabs = slices.DeleteFunc(tpl.Tabs, isServiceTab) // services mode; --tabs mode reruns them too
	fmt.Fprintf(stdout, "dry run: %s is open (workspace %s); services are not checked\n", project, id)
	if err := relaunchUp(d, project, id, tpl, relaunch.Options{Resume: resume, DryRun: true, Create: true}, stdout); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	}
	return 0
}

// errUpBusy and upLock are the relaunch core's project lock (one up per
// project at a time; the self-repair watcher takes it too).
var (
	errUpBusy = relaunch.ErrBusy
	upLock    = relaunch.ProjectLock
)

// upCacheDir is ~/.cache/agentbus (or $XDG_CACHE_HOME/agentbus).
func upCacheDir(env func(string) string) string { return relaunch.CacheDir(env) }

// procStartFn is the dispatcher's process identity, where the system lists
// processes with a start time: Linux (/proc) and macOS (sysctl, self-repair
// step 10). Elsewhere a snapshot proves nothing and the panes decide.
func procStartFn() func(int) (uint64, error) { return procStartFnFor(runtime.GOOS) }

func procStartFnFor(goos string) func(int) (uint64, error) {
	if goos != "linux" && goos != "darwin" {
		return nil
	}
	return dispatch.ProcStart
}

// readIntent is role's swap intent on the broker, if one is held, bounded.
func readIntent(project, role string) (bus.SwapIntent, bool, error) {
	c, err := bus.Connect("")
	if err != nil {
		return bus.SwapIntent{}, false, err
	}
	defer c.Close()
	b, err := bus.Open(c, project)
	if err != nil {
		return bus.SwapIntent{}, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return b.SwapIntentOf(ctx, role)
}

// readAgents is the project's {p}:agents, bounded.
func readAgents(project string) (map[string]bus.AgentSnapshot, error) {
	c, err := bus.Connect("")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	b, err := bus.Open(c, project)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return b.Agents(ctx)
}

// upHook is what herdr's workspace event runs for a known project's open
// workspace: it starts the project's stopped services as they are installed.
// No unit or environment is rewritten (a binary at another path would
// otherwise reconfigure and restart a running service), and no pane is
// touched: after a reboot herdr's native session restore relaunches the agents
// in the shells it has just spawned, and `agentbus up` is the person's gesture
// for an agent that died otherwise.
func upHook(d upDeps, project, ws string, stdout, stderr io.Writer) int {
	if d.Services == nil {
		return 0
	}
	if d.SuspendPause != nil {
		// A pause it cannot read keeps today's behaviour: the broker not
		// answering is the crash case, where starting the services is right.
		if rec, err := d.SuspendPause(project); err == nil && rec != nil && rec.Suspended() {
			return hookSuspended(d, project, rec, stdout, stderr)
		}
	}
	m, why := d.Services()
	if m == nil {
		fmt.Fprintf(stdout, "%s: no service manager (%s)\n", project, why)
		return 0
	}
	code := 0
	for _, k := range service.Kinds {
		st, err := m.State(project, k)
		if err != nil || !st.Installed || st.Active {
			continue
		}
		_ = service.Budget{Path: d.Paths.BudgetFile(project, k)}.Reset()
		if err := m.Start(project, k); err != nil {
			fmt.Fprintf(stderr, "agentbus: %s: %s: start: %v\n", project, k, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "%s: %s started (it was stopped)\n", project, k)
	}
	return code
}

// hookSuspended is the hook on a suspended project (spec §8): it starts no
// service by itself. With suspend.auto_resume on, and only on this host after
// a new boot (a focus before the reboot never wakes the team), it starts the
// full resume detached, as the person (identity.as), and returns at once;
// never --partial.
func hookSuspended(d upDeps, project string, rec *bus.PauseRecord, stdout, stderr io.Writer) int {
	if d.AutoResume == nil || !d.AutoResume() {
		fmt.Fprintf(stdout, "%s: suspended since %s: agentbus resume brings it back\n", project, time.UnixMilli(rec.At).Format("Jan 2 15:04"))
		return 0
	}
	host := ""
	if d.Hostname != nil {
		host, _ = d.Hostname()
	}
	boot := ""
	if d.Boot != nil {
		boot = d.Boot()
	}
	if host == "" || rec.Host != host || rec.Boot == "" || boot == "" || rec.Boot == boot {
		fmt.Fprintf(stdout, "%s: suspended: agentbus resume brings it back\n", project)
		return 0
	}
	if d.Person == nil || !bus.ValidName(d.Person()) {
		fmt.Fprintf(stdout, "%s: suspended, not resumed: no name is recorded for you to resume it as (agentbus identity set <your-name>)\n", project)
		return 0
	}
	if d.SpawnResume == nil {
		return 0
	}
	unlock := func() {}
	if d.Lock != nil {
		u, err := d.Lock(project, false)
		if errors.Is(err, errUpBusy) {
			fmt.Fprintf(stdout, "%s: resume already running\n", project)
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentbus: %s: %v: not resumed\n", project, err)
			return 1
		}
		unlock = u
	}
	// One try per boot and suspend (foureyes): a resume that stays suspended
	// is not started again on every focus, each with its own notice. The
	// mark is read and written under the lock, which is released before the
	// spawn: the resume takes it without waiting, and the mark, not the
	// lock, keeps a second event from starting another (reviewer-codex,
	// foureyes). One that cannot be written starts nothing.
	dir := d.Paths.StateDir(project)
	mark := autoResumeMark{Boot: boot, At: rec.At}
	if autoResumeTried(filepath.Join(dir, autoResumeFile), mark) {
		unlock()
		fmt.Fprintf(stdout, "%s: suspended: the auto-resume on this boot did not complete (agentbus resume brings it back; see %s)\n",
			project, filepath.Join(dir, "suspend.log"))
		return 0
	}
	err := writeAutoResumeMark(dir, mark)
	unlock()
	if err != nil {
		fmt.Fprintf(stderr, "agentbus: %s: recording the auto-resume: %v: not resumed (agentbus resume brings it back)\n", project, err)
		return 1
	}
	log := filepath.Join(dir, "suspend.log")
	if err := d.SpawnResume(project, log); err != nil {
		fmt.Fprintf(stderr, "agentbus: %s: starting the resume: %v (agentbus resume brings it back)\n", project, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: suspended before this boot: resuming (log: %s)\n", project, log)
	return 0
}

// autoResumeFile records the auto-resume the hook started: the boot and the
// suspend it was for.
const autoResumeFile = "auto-resume.json"

type autoResumeMark struct {
	Boot string `json:"boot"`
	At   int64  `json:"at"` // the suspend's at
}

// autoResumeTried: the hook already started the auto-resume of this suspend on
// this boot. An unreadable mark reads as not tried.
func autoResumeTried(path string, m autoResumeMark) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var old autoResumeMark
	return json.Unmarshal(raw, &old) == nil && old == m
}

// writeAutoResumeMark writes the mark 0600 in a 0700 directory, by a rename.
func writeAutoResumeMark(dir string, m autoResumeMark) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(m)
	tmp, err := os.CreateTemp(dir, ".auto-resume-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, autoResumeFile))
}

// checkClients (installer-no-client) refuses before any service starts or any
// tab opens only when none of the team's agent CLIs is found: then each
// client's install pointer and the next command, and false. With some
// missing it starts every tab as before and warns with their names (master:
// a lookup alone never drops a tab; their panes say if they fail). A CLI
// nobody could judge (errCLIUnknown) counts as found: fail open. It returns
// the executables confirmed absent, which open no trust gate.
func checkClients(d upDeps, project string, tpl upTemplate, stdout, stderr io.Writer) (map[string]bool, bool) {
	if d.TabExe == nil || d.LookPath == nil {
		return nil, true
	}
	absent := map[string]bool{}
	type miss struct {
		tab, exe string
		why      error
	}
	var missing []miss
	agents := 0
	for _, t := range tpl.Tabs {
		exe := d.TabExe(t.Command)
		if exe == "" {
			continue
		}
		agents++
		_, err := d.LookPath(exe)
		switch {
		case err == nil:
		case errors.Is(err, errCLIUnknown):
			fmt.Fprintf(stdout, "could not tell whether %s is installed (%v): %s starts anyway\n", exe, err, t.Name)
		default:
			missing = append(missing, miss{t.Name, exe, err})
			absent[exe] = true
		}
	}
	if len(missing) == 0 {
		return nil, true
	}
	if len(missing) == agents {
		var names []string
		for _, m := range missing {
			names = append(names, m.tab+" needs "+m.exe)
		}
		fmt.Fprintf(stderr, "agentbus up: no agent CLI on PATH for this team (%s; %v): nothing was started. Install at least one of:\n", strings.Join(names, ", "), missing[0].why)
		for _, l := range rolecfg.ClientGuidance() {
			fmt.Fprintln(stderr, "  "+l)
		}
		fmt.Fprintf(stderr, "busmon and the bus work meanwhile (busmon --project %s); once one is installed, then run: agentbus up %s\n", project, project)
		return absent, false
	}
	for _, m := range missing {
		where := ""
		for _, c := range rolecfg.Clients {
			if c.Exe == m.exe {
				where = " (" + c.Name + " (" + c.Exe + "): " + c.Install + ")"
			}
		}
		fmt.Fprintf(stdout, "warning: %s needs %s, not found%s: %v; its tab starts anyway and says so if it fails\n", m.tab, m.exe, where, m.why)
	}
	return absent, true
}

// errCLIAbsent: the user's login shell says the CLI is not installed; only
// this counts as missing (a warning, or the refusal when every agent's CLI
// is). errCLIUnknown: nobody could tell (no shell, a timeout, no answer): it
// counts as found, and the reason is said (foureyes).
var (
	errCLIAbsent  = errors.New("not installed")
	errCLIUnknown = errors.New("could not tell")
)

// lookAgentCLI finds an agent's CLI on up's PATH, else in the usual per-user
// install directories, else through the person's own login shell (shell):
// up may run from a pane (the herdr plugin's picker) whose PATH is narrower
// than the shell an agent's tab runs in, where nvm, volta, asdf, mise or a
// CLI's own installer put it on PATH. A miss is errCLIAbsent only when that
// shell says so; when it cannot answer, errCLIUnknown.
func lookAgentCLI(getenv func(string) string, look func(string) (string, error), shell func(string) (string, error)) func(string) (string, error) {
	return func(exe string) (string, error) {
		if p, err := look(exe); err == nil {
			return p, nil
		}
		var dirs []string
		if home := getenv("HOME"); home != "" {
			for _, d := range []string{".local/bin", "bin", ".npm-global/bin", ".bun/bin", ".cargo/bin"} {
				dirs = append(dirs, filepath.Join(home, d))
			}
		}
		for _, dir := range append(dirs, "/usr/local/bin", "/opt/homebrew/bin") {
			cand := filepath.Join(dir, exe)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return cand, nil
			}
		}
		p, err := shell(exe)
		switch {
		case err == nil:
			return p, nil
		case errors.Is(err, errCLIAbsent):
			return "", err
		}
		return "", fmt.Errorf("%w: %v", errCLIUnknown, err)
	}
}

// probeDone ends the probe's own output with command -v's status, so a
// shell that fails before running it (rc error, unsupported invocation) is
// told from a clean miss (reviewer-codex).
const probeDone = "AGENTBUS_PROBE_DONE"

// shellProbe asks $SHELL, as a login interactive shell, where exe is
// (command -v), within timeout: the path command -v printed when its status
// is 0, errCLIAbsent when it ran and failed, any other error when the shell
// could not answer (no SHELL, a timeout, no marker). exe is passed as an
// argument, never spliced into the script; fish gets its own syntax.
func shellProbe(getenv func(string) string, timeout time.Duration) func(string) (string, error) {
	return func(exe string) (string, error) {
		sh := getenv("SHELL")
		if sh == "" {
			return "", errors.New("no SHELL to ask")
		}
		// POSIX shells give -c's first argument to $0, so a placeholder comes
		// first and the name is $1; fish puts every argument in $argv, so the
		// name alone is $argv[1] (reviewer-codex).
		args := []string{"-lic", `command -v -- "$1"; printf '\n` + probeDone + ` %s\n' "$?"`, "agentbus", exe}
		if filepath.Base(sh) == "fish" {
			args = []string{"-lic", `command -v -- $argv[1]; printf '\n` + probeDone + ` %s\n' $status`, exe}
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, sh, args...)
		cmd.WaitDelay = 500 * time.Millisecond
		// A session of its own, so no controlling terminal: an interactive
		// shell's job control (tcsetpgrp) can never take the terminal from a
		// foreground herdr that start runs meanwhile (foureyes).
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		out, _ := cmd.Output() // the shell's own exit status is not the answer: the marker is
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s did not answer within %s", filepath.Base(sh), timeout)
		}
		lines := strings.Split(string(out), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			status, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), probeDone+" ")
			if !ok {
				continue
			}
			if status != "0" {
				return "", fmt.Errorf("%w (checked with %s)", errCLIAbsent, filepath.Base(sh))
			}
			for k := i - 1; k >= 0; k-- {
				if l := strings.TrimSpace(lines[k]); l != "" {
					return l, nil
				}
			}
			return "", errors.New("command -v found it but printed nothing")
		}
		return "", fmt.Errorf("%s gave no answer (it may have failed while starting)", filepath.Base(sh))
	}
}

// Broker readiness (up-broker-ready): how long up waits for a broker that is still starting.
// The budget is a deadline on the clock: the pings' own time counts, and each one is given at
// most brokerAttempt and never more than what is left.
const (
	brokerPoll    = 250 * time.Millisecond
	brokerAttempt = 2 * time.Second
	brokerWait    = 10 * time.Second
)

// waitBroker pings until the broker answers, for at most brokerWait. Only what a wait can
// fix is retried: LOADING (the dataset is being read back) and a connection that is refused or
// dropped (the container is starting). Any other answer returns at once: a wrong password, and
// a timeout too, since a starting local container refuses, it does not black-hole (a wrong
// host or a dead tunnel would only wait the whole budget; foureyes). A nil ping waits for nothing.
func waitBroker(ping func(time.Duration) error, sleep func(time.Duration), now func() time.Time) error {
	if ping == nil {
		return nil
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(brokerWait)
	var err, starting error // starting: the last answer a wait could fix
	for {
		left := deadline.Sub(now())
		if left <= 0 {
			return err
		}
		if err = ping(min(left, brokerAttempt)); err == nil {
			return nil
		}
		if !brokerStarting(err) {
			if starting != nil && errors.Is(err, context.DeadlineExceeded) {
				return starting // the budget cut the last ping short: say what the broker said
			}
			return err
		}
		starting = err
		if left = deadline.Sub(now()); left > 0 {
			sleep(min(brokerPoll, left))
		}
	}
}

func brokerStarting(err error) bool {
	return strings.HasPrefix(err.Error(), "LOADING") || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF)
}

// shellWord is a as one shell word: as typed when every character is safe
// (letters, digits and _ . / : = @ % + , ~ -), else single-quoted, so a
// suggested command pasted back runs that command and nothing else.
func shellWord(a string) string {
	if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=@%+,~-") == "" {
		return a
	}
	return shellQuote(a)
}

// upCallerEnv tells scripts/bootstrap that agentbus up runs it, so its recall
// does not tell the person to run agentbus up (stranger-test).
const upCallerEnv = "AGENTBUS_UP=1"

// bootstrapCommand is the repository's bootstrap with args, in up's environment
// plus upCallerEnv.
func bootstrapCommand(repo string, args []string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(repo, "scripts", "bootstrap"), args...)
	cmd.Env = append(os.Environ(), upCallerEnv)
	return cmd
}

func itoa(n int) string { return strconv.Itoa(n) }
