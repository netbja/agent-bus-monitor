package main

import (
	"bufio"
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
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
)

// startDeps is what agentbus start touches, injected: a test runs no up,
// reads no bus and writes only its own temporary files.
type startDeps struct {
	Env      func(string) string
	Manifest func() (*rolecfg.Manifest, error)
	Up       func(args []string) int // up's own code path
	Live     func(project string) (map[string]life, error)
	Repo     func() (string, error) // the checkout bootstrap writes templates from
	TTY      func() io.ReadWriter   // the person's terminal; nil when there is none
	Look     func(exe string) (string, error)
	Out, Err io.Writer

	// Outside herdr (spec "Outside herdr"): start launches the session itself.
	HerdrOnPath   func() bool                                                            // nil: not checked
	Herdr         func(session string, tty io.ReadWriter) (wait func() error, err error) // herdr --session on that terminal
	SessionSocket func(session string) (string, error)                                   // herdr session list --json, until running
	UpInSession   func(args []string, session, socket, ignoreWorkspace string, log io.Writer) int
	// start-tabs: herdr's first workspace of a session start created (one bare
	// shell tab) is not the project's. nil SessionRunning: never touched.
	SessionRunning   func(session string) (bool, error)
	InitialWorkspace func(socket string) (string, error)
	CloseInitial     func(socket, ws string) (string, error)
	Trusted          func(client, dir string) bool // nil: no trust line
	LogPath          func(project string) (string, error)
}

// realStartDeps runs up's own code path, reads the manifest as the role
// scripts do, and reads liveness from the project's bus.
func realStartDeps(stdout, stderr io.Writer) startDeps {
	return startDeps{
		Env: os.Getenv,
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(os.Environ())
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		},
		Up:   func(args []string) int { return runUp(args, realUpDeps(stdout, stderr), stdout, stderr) },
		Repo: func() (string, error) { return repoDir(os.Getenv) },
		TTY: func() io.ReadWriter {
			if t := setup.OpenTTY(); t != nil {
				return t
			}
			return nil
		},
		Look: realUpDeps(stdout, stderr).LookPath,
		HerdrOnPath: func() bool {
			_, err := exec.LookPath(herdrBin(os.Getenv))
			return err == nil
		},
		Herdr: func(session string, tty io.ReadWriter) (func() error, error) {
			f, ok := tty.(*os.File)
			if !ok {
				return nil, errors.New("no terminal to give herdr")
			}
			cmd := exec.Command(herdrBin(os.Getenv), "--session", session)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f // the person's terminal, for herdr's whole run
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd.Wait, nil
		},
		SessionSocket: herdrSessionSocket,
		UpInSession: func(args []string, session, socket, ignoreWorkspace string, log io.Writer) int {
			// up opens a workspace only inside herdr: the three a herdr pane
			// gives a plugin, set for this process and the herdr calls it makes.
			for k, v := range map[string]string{"HERDR_ENV": "1", "HERDR_SESSION": session, "HERDR_SOCKET_PATH": socket} {
				_ = os.Setenv(k, v)
			}
			deps := realUpDepsIn(nil, log, log) // no terminal for bootstrap: herdr owns it
			deps.IgnoreWorkspace = ignoreWorkspace
			return runUp(args, deps, log, log)
		},
		SessionRunning: herdrSessionRunning,
		InitialWorkspace: func(socket string) (string, error) {
			deadline := time.Now().Add(3 * time.Second) // herdr makes it as it starts
			for {
				ws, err := initialWorkspace(herdrIn(socket))
				if ws != "" || time.Now().After(deadline) {
					return ws, err
				}
				time.Sleep(250 * time.Millisecond)
			}
		},
		CloseInitial: func(socket, ws string) (string, error) { return closeInitialWorkspace(herdrIn(socket), ws) },
		Trusted:      clientTrusted(os.Getenv),
		LogPath: func(project string) (string, error) {
			paths, err := service.PathsFrom(os.Getenv)
			if err != nil {
				return "", err
			}
			dir := paths.StateDir(project)
			if !filepath.IsAbs(dir) {
				return "", fmt.Errorf("the state directory %q is not absolute", dir)
			}
			return filepath.Join(dir, "start.log"), nil
		},
		Live: func(project string) (map[string]life, error) {
			client, err := bus.Connect("")
			if err != nil {
				return nil, err
			}
			defer client.Close()
			b, err := bus.Open(client, project)
			if err != nil {
				return nil, err
			}
			snaps, err := b.Agents(context.Background())
			if err != nil {
				return nil, err
			}
			out := map[string]life{}
			for name, s := range snaps {
				out[name] = snapLife(s, true, dispatch.ProcStart)
			}
			return out, nil
		},
		Out: stdout, Err: stderr,
	}
}

// herdrSessionSocket asks herdr where a session lives (herdr session list
// --json: name, running, socket_path), every 250 ms until it is listed
// running, for 15 s at most. agentbus never computes herdr's config
// directory: herdrSession only parses a path it is given.
func herdrSessionSocket(session string) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, err := exec.Command(herdrBin(os.Getenv), "session", "list", "--json").Output()
		if err == nil {
			var list struct {
				Sessions []struct {
					Name       string `json:"name"`
					Running    bool   `json:"running"`
					SocketPath string `json:"socket_path"`
				} `json:"sessions"`
			}
			if json.Unmarshal(out, &list) == nil {
				for _, s := range list.Sessions {
					if s.Name == session && s.Running && s.SocketPath != "" {
						return s.SocketPath, nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("herdr session %s did not start within 15s", session)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// herdrSessionRunning reads once whether herdr lists the session running.
func herdrSessionRunning(session string) (bool, error) {
	out, err := exec.Command(herdrBin(os.Getenv), "session", "list", "--json").Output()
	if err != nil {
		return false, err
	}
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return false, err
	}
	for _, s := range list.Sessions {
		if s.Name == session {
			return s.Running, nil
		}
	}
	return false, nil
}

// herdrIn runs herdr against one session's socket.
func herdrIn(socket string) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		cmd := exec.Command(herdrBin(os.Getenv), args...)
		cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH="+socket)
		return cmd.Output()
	}
}

type herdrWorkspace struct {
	Label       string `json:"label"`
	WorkspaceID string `json:"workspace_id"`
	TabCount    int    `json:"tab_count"`
	PaneCount   int    `json:"pane_count"`
}

func herdrWorkspaces(herdr func(args ...string) ([]byte, error)) ([]herdrWorkspace, error) {
	out, err := herdr("workspace", "list")
	if err != nil {
		return nil, fmt.Errorf("herdr workspace list: %w", err)
	}
	var v struct {
		Result struct {
			Workspaces []herdrWorkspace `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("herdr workspace list: %w", err)
	}
	return v.Result.Workspaces, nil
}

// initialWorkspace is the first workspace herdr made for a new session: the
// session's only workspace, with one tab and one pane; else "".
func initialWorkspace(herdr func(args ...string) ([]byte, error)) (string, error) {
	ws, err := herdrWorkspaces(herdr)
	if err != nil || len(ws) != 1 || ws[0].TabCount != 1 || ws[0].PaneCount != 1 {
		return "", err
	}
	return ws[0].WorkspaceID, nil
}

// closeInitialWorkspace closes herdr's first workspace ws only while it is
// still untouched, read again right before: one tab, one pane, a plain shell
// in it (paneShell, argv only). It never types into the pane; anything else
// keeps it and the note says why.
func closeInitialWorkspace(herdr func(args ...string) ([]byte, error), ws string) (string, error) {
	all, err := herdrWorkspaces(herdr)
	if err != nil {
		return "", err
	}
	var w *herdrWorkspace
	for i := range all {
		if all[i].WorkspaceID == ws {
			w = &all[i]
		}
	}
	switch {
	case w == nil:
		return fmt.Sprintf("herdr's first workspace %s is gone already", ws), nil
	case w.TabCount != 1 || w.PaneCount != 1:
		return fmt.Sprintf("kept herdr's first workspace %s: it has %d tabs and %d panes now", ws, w.TabCount, w.PaneCount), nil
	}
	out, err := herdr("pane", "list")
	if err != nil {
		return "", fmt.Errorf("herdr pane list: %w", err)
	}
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &panes); err != nil {
		return "", fmt.Errorf("herdr pane list: %w", err)
	}
	pane := ""
	for _, p := range panes.Result.Panes {
		if p.WorkspaceID == ws {
			pane = p.PaneID
		}
	}
	if pane == "" {
		return fmt.Sprintf("kept herdr's first workspace %s: its pane is not listed", ws), nil
	}
	if plain, err := paneShell(herdr, pane); err != nil || !plain {
		return fmt.Sprintf("kept herdr's first workspace %s: something runs in it, or it could not be read", ws), nil
	}
	if _, err := herdr("workspace", "close", ws); err != nil {
		return "", fmt.Errorf("herdr workspace close %s: %w", ws, err)
	}
	return fmt.Sprintf("closed herdr's first workspace %s (a bare shell): busmon's tab is the first", ws), nil
}

// startUsage is start's line in the help table.
const startUsage = "usage: agentbus start [<path>] [--yes] [--dry-run] [--name <project>] [--as <name>] [--roles <role[:profile],...>]"

// runStart is agentbus start (agentbus-start spec): deduce, show, let the
// person change, write the one or two things up cannot deduce, then run up's
// own code path, so every rule of up holds unchanged.
func runStart(args []string, d startDeps) int {
	args, yes := extractBool(args, "--yes")
	args, dryRun := extractBool(args, "--dry-run")
	args, name := extractFlag(args, "--name")
	args, as := extractFlag(args, "--as")
	args, roles := extractFlag(args, "--roles")
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(d.Err, startUsage)
		return 2
	}
	path := "."
	if len(args) == 1 {
		path = args[0]
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(d.Err, "agentbus start: "+format+"\n", a...)
		return 1
	}
	m, err := d.Manifest()
	if err != nil {
		return fail("role manifest: %v", err)
	}
	p, err := deduceStart(path, d.Env, m)
	if err != nil {
		return fail("%v", err)
	}

	// The flags pre-answer the assistant, checked by the same rules.
	chosenTeam := false
	if name != "" {
		if err := p.setProject(name, "--name", d.Env, m); err != nil {
			return fail("--name: %v", err)
		}
	}
	if as != "" {
		if !bus.ValidName(as) {
			return fail("--as: %q is not a name: a lowercase letter, then lowercase letters, digits, - or _, up to 32", as)
		}
		p.Name, p.NameWhy, p.NameAsk = as, "--as", ""
	}
	if roles != "" {
		team, err := m.ParseTeam(roles)
		if err != nil {
			return fail("--roles: %v", err)
		}
		p.Team, p.TeamWhy, chosenTeam = team, "--roles", true
	}
	for _, n := range p.Notes {
		fmt.Fprintln(d.Out, n)
	}
	// The person's terminal is opened once: the assistant's handle is the
	// one herdr gets, and whoever ends with it closes it (foureyes).
	var tty io.ReadWriter
	handed := false
	defer func() {
		if c, ok := tty.(io.Closer); ok && !handed {
			c.Close()
		}
	}()
	if !yes {
		if d.TTY != nil {
			tty = d.TTY()
		}
		if tty == nil {
			p.print(d.Out, m)
			fmt.Fprintln(d.Out, "\nNo terminal to ask on: run agentbus start again with --yes to take these values, or with --name, --as and --roles to change them.")
			return 2
		}
		a := &asker{in: bufio.NewReader(tty), out: tty, m: m, env: d.Env, look: d.Look}
		if a.look == nil {
			a.look = func(exe string) (string, error) { return exe, nil }
		}
		if p.Existing {
			a.live, _ = d.Live(p.Project) // unknown leaves the running line out
		}
		if err := a.run(&p); err != nil {
			fmt.Fprintln(d.Out, "nothing written")
			return 0
		}
		chosenTeam = chosenTeam || a.chosenTeam
	}
	switch {
	case p.ProjectAsk != "":
		return fail("%s: give the project's name with --name", p.ProjectAsk)
	case p.NameAsk != "":
		return fail("%s: give yours with --as", p.NameAsk)
	case p.TeamAsk != "" && roles == "":
		return fail("%s: give the team with --roles", p.TeamAsk)
	case !hasMaster(p.Team): // after the assistant, which can add it (reviewer-codex)
		return fail("master cannot be removed: it is the escalation target and the pilot")
	}

	// A team change on an existing project: its template must be the one
	// bootstrap wrote (a rewrite would erase a hand edit), and no live agent
	// may lose its tab or change its client (up's relaunch never closes one).
	// The template as written decides: a stale profile or a removed role's
	// tab is a change to write, even when the chosen team reads the same
	// once normalised (reviewer-codex).
	var onDisk []rolecfg.TeamRole
	if p.Existing {
		if onDisk, err = rawTeam(p.Template); err != nil {
			return fail("%v", err)
		}
	}
	change := p.Existing && teamKey(p.Team) != teamKey(onDisk)
	if change {
		if err := checkTeamChange(d, m, p); err != nil {
			return fail("%v", err)
		}
	}
	complete := teamKey(p.Team) == teamKey(completeTeam(m))
	if dryRun {
		p.print(d.Out, m)
		fmt.Fprintln(d.Out, "\n--dry-run: nothing written, nothing started.")
		return 0
	}

	outside := d.Env("HERDR_ENV") != "1"
	if outside && d.HerdrOnPath != nil && !d.HerdrOnPath() {
		return fail("herdr is not on PATH: the team runs in it. Install it from https://herdr.dev (or ./setup.sh install --install-herdr), then run agentbus start again")
	}
	// Outside herdr with a terminal, start runs herdr on it: where up's
	// output will go is settled before anything is written (foureyes).
	logPath := ""
	runHerdr := outside && d.TTY != nil && d.Herdr != nil
	if runHerdr && tty == nil {
		tty = d.TTY()
	}
	if runHerdr && tty != nil {
		if logPath, err = d.LogPath(p.Project); err != nil {
			return fail("where to log the team's opening: %v", err)
		}
	}

	// start's own writes: the name when none is recorded, the default team
	// when the person chose one.
	cfg := config.Path(d.Env)
	if !p.NameRecorded && p.Name != "" && cfg != "" {
		if err := config.Set(cfg, "identity.as", p.Name); err != nil {
			return fail("recording your name in %s: %v", cfg, err)
		}
		fmt.Fprintf(d.Out, "recorded your name (%s) in %s\n", p.Name, cfg)
	}
	if chosenTeam && cfg != "" {
		if complete {
			err = config.Unset(cfg, "start.team")
		} else {
			err = config.Set(cfg, "start.team", teamSpec(p.Team))
		}
		if err != nil {
			return fail("writing start.team in %s: %v", cfg, err)
		}
	}

	upArgs := []string{p.Project, p.Folder, "--as", p.Name}
	switch {
	case change:
		upArgs = append(upArgs, "new", "--roles", teamSpec(p.Team))
	case !p.Existing && !complete:
		upArgs = append(upArgs, "--roles", teamSpec(p.Team))
	}
	if !runHerdr || tty == nil { // inside herdr up opens the team; without a terminal it provisions and says how to open it
		return d.Up(upArgs)
	}
	if code := d.Up(upArgs); code != 0 { // provisioned as up does outside herdr
		return code
	}
	fmt.Fprintf(d.Out, "starting herdr --session %s now: the team opens inside it\n", p.Project)
	handed = true // startInHerdr closes it once herdr exits
	return startInHerdr(d, m, p, upArgs, tty, logPath)
}

// startInHerdr hands the terminal to herdr --session <project> and opens the
// team inside it through the session's socket (spec, "Outside herdr"). What
// up would say goes to start.log while herdr draws, so the lines a person
// needs are said before: the missing clients and the dialogs to answer.
func startInHerdr(d startDeps, m *rolecfg.Manifest, p startPlan, upArgs []string, tty io.ReadWriter, logPath string) int {
	a := &asker{m: m, look: d.Look}
	if a.look == nil {
		a.look = func(exe string) (string, error) { return exe, nil }
	}
	for _, l := range a.clientLines(p.Team) {
		if strings.Contains(l, "✗") {
			fmt.Fprintln(d.Out, l)
		}
	}
	if d.Trusted != nil {
		seen := map[string]bool{}
		for _, r := range p.Team {
			prof, err := m.ResolveWith(r.Role, a.profileOf(r))
			if err != nil || seen[prof.Client] || d.Trusted(prof.Client, p.Folder) {
				continue
			}
			seen[prof.Client] = true
			fmt.Fprintf(d.Out, "%s does not trust %s yet: answer its dialog in the %s tab\n", clientLabel(prof.Client), p.Folder, r.Role)
		}
	}
	closeTTY := func() {
		if c, ok := tty.(io.Closer); ok {
			c.Close()
		}
	}
	// A session that already ran has no first workspace of ours to set aside;
	// unknown counts as running, so nothing is ever closed on a guess.
	fresh := false
	if d.SessionRunning != nil {
		running, err := d.SessionRunning(p.Project)
		fresh = err == nil && !running
	}
	wait, err := d.Herdr(p.Project, tty) // the terminal start opened, never its own stdio (reviewer-codex)
	if err != nil {
		closeTTY()
		fmt.Fprintf(d.Err, "agentbus start: starting herdr --session %s: %v\n", p.Project, err)
		return 1
	}
	// up opens the team beside herdr's run: herdr holds the terminal until
	// it exits, and up may wait on a trust dialog meanwhile.
	type opened struct {
		code    int
		sockErr error
	}
	done := make(chan opened, 1)
	go func() {
		socket, err := d.SessionSocket(p.Project)
		if err != nil {
			done <- opened{1, err}
			return
		}
		code := 1
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err == nil {
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				initial := ""
				if fresh && d.InitialWorkspace != nil {
					initial, _ = d.InitialWorkspace(socket)
				}
				code = d.UpInSession(upArgs, p.Project, socket, initial, f)
				// Once the team is open, herdr's first workspace (a bare shell
				// tab) goes, if it is still untouched; never typed into.
				if code == 0 && initial != "" && d.CloseInitial != nil {
					note, err := d.CloseInitial(socket, initial)
					if err != nil {
						note = fmt.Sprintf("kept herdr's first workspace %s: %v", initial, err)
					}
					fmt.Fprintln(f, note)
				}
				f.Close()
			}
		}
		done <- opened{code, nil}
	}()
	herdrErr := wait()
	closeTTY()
	var r opened
	select {
	case r = <-done:
	default: // herdr left first (foureyes): say why start still waits
		fmt.Fprintf(d.Err, "agentbus start: herdr has exited; still opening the team: see %s\n", logPath)
		r = <-done
	}
	code, sockErr := r.code, r.sockErr
	switch {
	case sockErr != nil:
		fmt.Fprintf(d.Err, "agentbus start: %v; inside it, run: agentbus up %s\n", sockErr, p.Project)
		return 1
	case code != 0:
		fmt.Fprintf(d.Err, "agentbus start: opening the team inside herdr failed: see %s\n", logPath)
		return 1
	case herdrErr != nil:
		var exit *exec.ExitError
		if errors.As(herdrErr, &exit) {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

// setProject takes a name the person gave: valid, and never another folder's
// project; nothing changes on a refusal (foureyes). An existing project of
// this folder brings its template's team (reviewer-codex).
func (p *startPlan) setProject(name, why string, env func(string) string, m *rolecfg.Manifest) error {
	if !bus.ValidName(name) {
		return fmt.Errorf("%q is not a project name: a lowercase letter, then lowercase letters, digits, - or _, up to 32", name)
	}
	rows, _ := projlist.Templates(rolecfg.ProjectsDir(env))
	existing := false
	for _, r := range rows {
		if r.Name != name {
			continue
		}
		if dir, ok := canonDir(expandHome(r.WorkingDir, env("HOME"))); !ok || dir != p.Folder {
			return fmt.Errorf("%s is the project of %s: choose another name", name, r.WorkingDir)
		}
		existing = true
	}
	template := filepath.Join(rolecfg.ProjectsDir(env), name+".toml")
	if existing {
		team, stale, err := templateTeam(template, m)
		if err != nil {
			return err
		}
		p.Team, p.TeamWhy, p.TeamAsk = team, "its template", ""
		if len(stale) > 0 {
			p.TeamAsk = strings.Join(stale, ", ") + ": no longer allowed by roles.toml"
		}
	}
	p.Project, p.ProjectWhy, p.ProjectAsk, p.Existing, p.Template = name, why, "", existing, template
	return nil
}

// hasMaster: a team must hold master, the escalation target and the pilot.
func hasMaster(team []rolecfg.TeamRole) bool {
	return slices.ContainsFunc(team, func(r rolecfg.TeamRole) bool { return r.Role == "master" })
}

// checkTeamChange refuses a team change on a template edited by hand, and
// one that drops or moves a live agent.
func checkTeamChange(d startDeps, m *rolecfg.Manifest, p startPlan) error {
	data, err := os.ReadFile(p.Template)
	if err != nil {
		return err
	}
	repo, err := d.Repo()
	if err != nil {
		return err
	}
	var tpl upTemplate
	if _, err := toml.Decode(string(data), &tpl); err != nil {
		return fmt.Errorf("reading %s: %v", p.Template, err)
	}
	if string(data) != regenerate(m, p, tpl, repo) {
		return fmt.Errorf("%s was edited by hand: change its tabs there, or move it aside to let start write it", p.Template)
	}
	live, err := d.Live(p.Project)
	if err != nil {
		return fmt.Errorf("cannot tell which agents of %s are live (%v): nothing changed", p.Project, err)
	}
	onDisk, err := rawTeam(p.Template) // every agent tab, a removed role's too
	if err != nil {
		return err
	}
	for _, old := range onDisk {
		i := slices.IndexFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == old.Role })
		state, known := live[old.Role]
		if !known || state == lifeDead || (i >= 0 && p.Team[i].Profile == old.Profile) {
			continue
		}
		why := "is live"
		if state != lifeAlive { // snapLife proves nothing off Linux (foureyes)
			why = "may still run (its process cannot be checked)"
		}
		return fmt.Errorf("%s %s: run agentbus retire %s first (up never closes a running agent's tab)", old.Role, why, old.Role)
	}
	return nil
}

// regenerate is the template bootstrap would write for the template's own
// team and settings: equal bytes mean nobody edited it.
func regenerate(m *rolecfg.Manifest, p startPlan, tpl upTemplate, repo string) string {
	var manifest, scripts, as string
	dispatch, missions := false, false
	for _, t := range tpl.Tabs {
		words, _ := rolecfg.ShellSplit(t.Command)
		switch t.Name {
		case "busmon":
			if i := slices.Index(words, "--as"); i >= 0 && i+1 < len(words) {
				as = words[i+1]
			}
		case "dispatch":
			dispatch = true
		case "missions":
			missions = true
		}
		if mf, role, _ := tabLaunch(t.Command); role != "" && manifest == "" {
			manifest = mf
			for _, w := range words {
				if filepath.Base(w) == "agent-launch" {
					scripts = filepath.Dir(w)
				}
			}
		}
	}
	return rolecfg.TemplateTeam(m, manifest, p.Project, tpl.WorkingDir, scripts, repo, as, dispatch, missions, tabsTeam(tpl))
}

// rawTeam is a template's agent tabs as written: every role, known to
// roles.toml or not, on the --profile its tab names, allowed or not.
func rawTeam(path string) ([]rolecfg.TeamRole, error) {
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	return tabsTeam(tpl), nil
}

func tabsTeam(tpl upTemplate) []rolecfg.TeamRole {
	var team []rolecfg.TeamRole
	for _, t := range tpl.Tabs {
		if _, role, profile := tabLaunch(t.Command); role != "" {
			team = append(team, rolecfg.TeamRole{Role: role, Profile: profile})
		}
	}
	return team
}

// completeTeam is every boot role on its own profile.
func completeTeam(m *rolecfg.Manifest) []rolecfg.TeamRole {
	var team []rolecfg.TeamRole
	for _, role := range m.RolesByTier("boot") {
		team = append(team, rolecfg.TeamRole{Role: role})
	}
	return team
}

// teamSpec is a team in --roles syntax.
func teamSpec(team []rolecfg.TeamRole) string {
	var items []string
	for _, r := range team {
		if r.Profile != "" {
			items = append(items, r.Role+":"+r.Profile)
		} else {
			items = append(items, r.Role)
		}
	}
	return strings.Join(items, ",")
}

// teamKey compares two teams whatever their order.
func teamKey(team []rolecfg.TeamRole) string {
	items := strings.Split(teamSpec(team), ",")
	slices.Sort(items)
	return strings.Join(items, ",")
}

// print shows the deduced values, one per line, as the assistant does.
func (p startPlan) print(out io.Writer, m *rolecfg.Manifest) {
	fmt.Fprintf(out, "agentbus start — %s\n\n", p.Folder)
	project, name := p.Project, p.Name
	if p.ProjectAsk != "" {
		project = "? (" + p.ProjectAsk + ")"
	}
	if p.NameAsk != "" {
		name = "? (" + p.NameAsk + ")"
	}
	why := func(s string) string {
		if s == "" {
			return ""
		}
		return "  (" + s + ")"
	}
	fmt.Fprintf(out, "  Project   %s%s\n", project, why(p.ProjectWhy))
	fmt.Fprintf(out, "  Folder    %s\n", p.Folder)
	fmt.Fprintf(out, "  You       %s%s\n", name, why(p.NameWhy))
	fmt.Fprintf(out, "  Team      %s%s\n", strings.ReplaceAll(teamSpec(p.Team), ",", " "), why(p.TeamWhy))
	if p.TeamAsk != "" {
		fmt.Fprintf(out, "            ? %s\n", p.TeamAsk)
	}
}
