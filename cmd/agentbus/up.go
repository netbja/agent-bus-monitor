package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// upDeps is what `agentbus up` touches, injected for tests.
type upDeps struct {
	Herdr     func(args ...string) ([]byte, error) // the herdr CLI
	Bootstrap func(args []string) error            // scripts/bootstrap <args>
	Env       func(string) string

	// The project services (v0.12). Services finds the manager, or says why
	// there is none; nil means none.
	Services func() (service.Manager, string)
	Paths    service.Paths
	Exe      func() (string, error) // this agentbus, which the units run
	Environ  []string               // the service environment is picked from it
	Mark     func(project string, inst bus.ServicesInstall) error
	Host     string
}

type upTab struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

type upTemplate struct {
	Name       string  `toml:"name"`
	WorkingDir string  `toml:"working_dir"`
	Tabs       []upTab `toml:"tabs"`
}

// runUp provisions a project (bootstrap, idempotent) and, inside herdr, opens
// its workspace directly — busmon first and focused, then every tab of the
// template — or focuses it when it is already open. It replaces "bootstrap new,
// then Ctrl+B Up and pick the project". Outside herdr it provisions and says how
// to open it.
func runUp(args []string, d upDeps, stdout, stderr io.Writer) int {
	if len(args) == 0 || !bus.ValidName(args[0]) {
		fmt.Fprintln(stderr, "usage: agentbus up <project> [<path>] [--as <your-name>] [--tabs] [bootstrap flags: --no-dispatch --no-missions --session <s> --index --cron --yes]")
		return 1
	}
	project := args[0]
	args, tabs := extractBool(args, "--tabs")
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
	failed := false
	if m != nil {
		if err := startServices(d, m, project, tpl.WorkingDir, herdrSession(args, d.Environ), kinds, stdout); err != nil {
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
		fmt.Fprintf(stdout, "provisioned %s. To open it, start herdr (herdr --session %s), then run: agentbus up %s\n", project, project, project)
		return code
	}
	if id, err := openWorkspace(d, project); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	} else if id != "" {
		fmt.Fprintf(stdout, "%s is already open: focused workspace %s\n", project, id)
		return code
	}
	if len(tpl.Tabs) == 0 {
		fmt.Fprintf(stderr, "agentbus up: %s has no tabs\n", path)
		return 1
	}
	if err := layout(d, project, tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "opened %s: %d tabs, busmon first (reopen later with Ctrl+B then Up)\n", project, len(tpl.Tabs))
	return code
}

// herdrSession is the session the project opens in, as bootstrap resolves
// it: --session, else the caller's HERDR_SESSION, else the project name.
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

// errNotInstalled: up refused to write a unit at all.
var errNotInstalled = errors.New("services not installed")

// startServices writes the environment file, the marker and the units, then
// starts each service that is not already running with this configuration.
func startServices(d upDeps, m service.Manager, project, workdir, session string, kinds []string, stdout io.Writer) error {
	exe, err := d.Exe()
	if err != nil {
		return fmt.Errorf("%w: cannot find this agentbus: %v", errNotInstalled, err)
	}
	if strings.Contains(exe, "go-build") || !filepath.IsAbs(exe) {
		return fmt.Errorf("%w: a unit must run an installed agentbus, not %s (./setup.sh install, then run that one)", errNotInstalled, exe)
	}
	env := service.CollectEnv(d.Environ)
	if env["HERDR_SESSION"] != session { // another session's socket would reach the wrong server
		delete(env, "HERDR_SOCKET_PATH")
	}
	env["HERDR_SESSION"] = session
	envChanged, err := service.WriteEnvFile(d.Paths.EnvFile(project), env)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotInstalled, err)
	}
	if err := d.Mark(project, bus.ServicesInstall{Kinds: kinds, Manager: m.Name(), Host: d.Host}); err != nil {
		return fmt.Errorf("%w: marking %s: %v", errNotInstalled, bus.ServicesKey(project), err)
	}
	var errs []error
	for _, k := range kinds {
		spec := service.Spec{Project: project, Kind: k, Exe: exe, WorkDir: workdir, Paths: d.Paths, EnvFile: d.Paths.EnvFile(project)}
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
	return errors.Join(errs...)
}

// openWorkspace focuses an already open workspace labelled project and returns
// its id, or "" when there is none.
func openWorkspace(d upDeps, project string) (string, error) {
	out, err := d.Herdr("workspace", "list")
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
		if w.Label == project {
			if _, err := d.Herdr("workspace", "focus", w.WorkspaceID); err != nil {
				return "", fmt.Errorf("herdr workspace focus: %w", err)
			}
			return w.WorkspaceID, nil
		}
	}
	return "", nil
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
	if err := fill(d, ws, created.Result.Tab.TabID, panes, tpl); err != nil {
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
	for i, t := range tpl.Tabs {
		if strings.TrimSpace(t.Command) == "" {
			continue
		}
		if _, err := d.Herdr("pane", "run", panes[i], t.Command); err != nil {
			return fmt.Errorf("herdr pane run (%s): %w", t.Name, err)
		}
	}
	return nil
}

// templatePath is where bootstrap writes the project's herdr-plus template
// (scripts/bootstrap projects_dir).
func templatePath(d upDeps, project string) string {
	dir := d.Env("HERDR_PLUS_PROJECTS_DIR")
	if dir == "" {
		if out, err := d.Herdr("plugin", "config-dir", "cloudmanic.herdr-plus"); err == nil && strings.TrimSpace(string(out)) != "" {
			dir = filepath.Join(strings.TrimSpace(string(out)), "projects")
		}
	}
	if dir == "" {
		cfg := d.Env("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(d.Env("HOME"), ".config")
		}
		dir = filepath.Join(cfg, "herdr-plus", "projects")
	}
	return filepath.Join(dir, project+".toml")
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
func realUpDeps(stdout, stderr io.Writer) upDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	host, _ := os.Hostname()
	return upDeps{
		Herdr: func(args ...string) ([]byte, error) {
			bin := os.Getenv("HERDR_BIN")
			if bin == "" {
				bin = "herdr"
			}
			cmd := exec.Command(bin, args...)
			cmd.Stderr = stderr
			return cmd.Output()
		},
		Bootstrap: func(args []string) error {
			repo, err := repoDir(os.Getenv)
			if err != nil {
				return err
			}
			cmd := exec.Command(filepath.Join(repo, "scripts", "bootstrap"), args...)
			cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, os.Stdin
			return cmd.Run()
		},
		Env: os.Getenv,
		Services: func() (service.Manager, string) {
			return service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		},
		Paths: paths,
		Exe: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(exe)
		},
		Environ: os.Environ(),
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
	}
}
