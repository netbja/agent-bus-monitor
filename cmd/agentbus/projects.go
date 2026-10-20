package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// projectsDeps are the sources of `agentbus projects`, each allowed to fail.
type projectsDeps struct {
	Env func(string) string
	// Keys lists a project's broker keys; nil when the broker cannot be dialled.
	Keys projlist.KeysFunc
	// Broker lists the broker's projects (bus.Projects); nil like Keys.
	Broker func(ctx context.Context) ([]string, error)
	// Agents reads a project's {p}:agents.
	Agents    func(project string) (map[string]bus.AgentSnapshot, error)
	ProcStart func(int) (uint64, error)
	// Services finds the manager; nil means none.
	Services func() (service.Manager, string)
}

// projectLine is one project as `agentbus projects --json` prints it.
type projectLine struct {
	Name     string `json:"name"`
	Dir      string `json:"dir,omitempty"`
	Template bool   `json:"template"`
	Broker   string `json:"broker"`   // present, absent, ?
	Services string `json:"services"` // running, stopped, none, ?
	Alive    int    `json:"alive"`
	Dead     int    `json:"dead"`
	Unknown  int    `json:"unknown"`
	Agents   bool   `json:"agents_known"` // false: the broker could not be read
}

// runProjects lists every known project: its template, the broker's answer,
// its services and its agents. It needs no project and works with Redis
// down (the columns say ?). Exit 0 even then.
func runProjects(args []string, d projectsDeps, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			fmt.Fprintln(stderr, "usage: agentbus projects [--json]")
			return 2
		}
	}
	rows, err := projlist.Templates(rolecfg.ProjectsDir(d.Env))
	if err != nil {
		fmt.Fprintf(stderr, "agentbus projects: %v\n", err)
	}
	var m service.Manager
	if d.Services != nil {
		m, _ = d.Services()
	}
	byName := map[string]bool{}
	for _, r := range rows {
		byName[r.Name] = true
	}
	if m != nil { // a project with units but no template still shows
		if units, err := m.List(); err == nil {
			for _, u := range units {
				if !byName[u.Project] && bus.ValidName(u.Project) {
					byName[u.Project] = true
					rows = append(rows, projlist.Row{Name: u.Project})
				}
			}
		}
	}
	if d.Broker != nil { // a project only the broker knows (made on another machine) still shows
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		names, err := d.Broker(ctx)
		cancel()
		if err == nil {
			for _, n := range names {
				if !byName[n] && bus.ValidName(n) {
					byName[n] = true
					rows = append(rows, projlist.Row{Name: n})
				}
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	if d.Keys != nil {
		rows = projlist.Annotate(context.Background(), rows, d.Keys, 2*time.Second)
	}
	var lines []projectLine
	for _, r := range rows {
		l := projectLine{Name: r.Name, Dir: r.WorkingDir, Template: r.Path != "", Broker: r.Broker.String(), Services: "?"}
		if m != nil {
			l.Services = servicesWord(m, r.Name)
		}
		roles := templateRoles(r.Path, r.Name)
		if r.Broker == projlist.Present && d.Agents != nil {
			if snaps, err := d.Agents(r.Name); err == nil {
				l.Agents = true
				for _, role := range roles {
					s, has := snaps[role]
					switch snapLife(s, has, d.ProcStart) {
					case lifeAlive:
						l.Alive++
					case lifeDead:
						l.Dead++
					default:
						l.Unknown++
					}
				}
			}
		}
		lines = append(lines, l)
	}
	if asJSON {
		if lines == nil {
			lines = []projectLine{}
		}
		out, _ := json.MarshalIndent(lines, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	if len(lines) == 0 {
		fmt.Fprintf(stdout, "no project: no template in %s and no service installed\n", rolecfg.ProjectsDir(d.Env))
		fmt.Fprintln(stdout, "create one with: agentbus up <project> <path>")
		return 0
	}
	width := 7
	for _, l := range lines {
		width = max(width, len(l.Name))
	}
	fmt.Fprintf(stdout, "%-*s  %-8s  %-8s  %-22s  %s\n", width, "PROJECT", "BROKER", "SERVICES", "AGENTS", "DIRECTORY")
	var down []string
	for _, l := range lines {
		agents := "?"
		if l.Agents {
			agents = fmt.Sprintf("%d alive / %d dead", l.Alive, l.Dead)
			if l.Unknown > 0 {
				agents += fmt.Sprintf(" / %d ?", l.Unknown)
			}
		}
		dir := l.Dir
		if !l.Template {
			dir = "(no template)"
		}
		fmt.Fprintf(stdout, "%-*s  %-8s  %-8s  %-22s  %s\n", width, l.Name, l.Broker, l.Services, agents, dir)
		if l.Template && (l.Services == "stopped" || l.Dead > 0) {
			down = append(down, l.Name)
		}
	}
	for _, p := range down {
		fmt.Fprintf(stdout, "\n%s has something down: agentbus up %s (inside herdr) restarts it", p, p)
	}
	if len(down) > 0 {
		fmt.Fprintln(stdout)
	}
	return 0
}

// servicesWord: running when every installed service runs, stopped when one
// does not, none when nothing is installed, ? when the manager cannot tell.
func servicesWord(m service.Manager, project string) string {
	installed, stopped := 0, 0
	for _, k := range service.Kinds {
		st, err := m.State(project, k)
		if err != nil {
			return "?"
		}
		if st.Installed {
			installed++
			if !st.Active {
				stopped++
			}
		}
	}
	switch {
	case installed == 0:
		return "none"
	case stopped > 0:
		return "stopped"
	}
	return "running"
}

// templateRoles are the roles the template's tabs launch for project.
func templateRoles(path, project string) []string {
	if path == "" {
		return nil
	}
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		return nil
	}
	var roles []string
	for _, t := range tpl.Tabs {
		if r, ok := launchRole(t.Command, project); ok {
			roles = append(roles, r)
		}
	}
	return roles
}

func realProjectsDeps() projectsDeps {
	d := projectsDeps{
		Env:       os.Getenv,
		ProcStart: procStartFn(),
		Agents:    readAgents,
		Services: func() (service.Manager, string) {
			return service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		},
	}
	// The client lives as long as the command; a broker that is down answers
	// each probe with an error, which reads as ?.
	if c, err := bus.Connect(""); err == nil {
		d.Keys = func(ctx context.Context, p string) ([]string, error) { return bus.ProjectKeys(ctx, c, p) }
		d.Broker = func(ctx context.Context) ([]string, error) {
			sums, err := bus.Projects(ctx, c)
			var names []string
			for _, s := range sums {
				names = append(names, s.Project)
			}
			return names, err
		}
	}
	return d
}
