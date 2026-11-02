package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// startDeps is what agentbus start touches, injected: a test runs no up,
// reads no bus and writes only its own temporary files.
type startDeps struct {
	Env      func(string) string
	Manifest func() (*rolecfg.Manifest, error)
	Up       func(args []string) int // up's own code path
	Live     func(project string) (map[string]life, error)
	Repo     func() (string, error) // the checkout bootstrap writes templates from
	Out, Err io.Writer
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
		if err := p.setProject(name, d.Env); err != nil {
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
	if !slices.ContainsFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == "master" }) {
		return fail("master cannot be removed: it is the escalation target and the pilot")
	}
	for _, n := range p.Notes {
		fmt.Fprintln(d.Out, n)
	}
	if !yes {
		p.print(d.Out, m)
		fmt.Fprintln(d.Out, "\nNo terminal to ask on: run agentbus start again with --yes to take these values, or with --name, --as and --roles to change them.")
		return 2
	}
	switch {
	case p.ProjectAsk != "":
		return fail("%s: give the project's name with --name", p.ProjectAsk)
	case p.NameAsk != "":
		return fail("%s: give yours with --as", p.NameAsk)
	}

	// A team change on an existing project: its template must be the one
	// bootstrap wrote (a rewrite would erase a hand edit), and no live agent
	// may lose its tab or change its client (up's relaunch never closes one).
	change := p.Existing && teamKey(p.Team) != teamKey(templateTeam(p.Template))
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
	return d.Up(upArgs)
}

// setProject takes a name the person gave: valid, and never another folder's
// project.
func (p *startPlan) setProject(name string, env func(string) string) error {
	if !bus.ValidName(name) {
		return fmt.Errorf("%q is not a project name: a lowercase letter, then lowercase letters, digits, - or _, up to 32", name)
	}
	rows, _ := projlist.Templates(rolecfg.ProjectsDir(env))
	p.Project, p.ProjectWhy, p.ProjectAsk, p.Existing = name, "--name", "", false
	p.Template = filepath.Join(rolecfg.ProjectsDir(env), name+".toml")
	for _, r := range rows {
		if r.Name != name {
			continue
		}
		if expandHome(r.WorkingDir, env("HOME")) != p.Folder {
			return fmt.Errorf("%s is the project of %s: choose another name", name, r.WorkingDir)
		}
		p.Existing = true
	}
	return nil
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
	for _, old := range templateTeam(p.Template) {
		i := slices.IndexFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == old.Role })
		if live[old.Role] != lifeAlive || (i >= 0 && p.Team[i].Profile == old.Profile) {
			continue
		}
		return fmt.Errorf("%s is live: run agentbus retire %s first (up never closes a running agent's tab)", old.Role, old.Role)
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
	return rolecfg.TemplateTeam(m, manifest, p.Project, tpl.WorkingDir, scripts, repo, as, dispatch, missions, templateTeam(p.Template))
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
}
