package main

import (
	"fmt"
	"io"
	"slices"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/service"
)

// machineAtLogin is services.start_at_login from the machine's config.toml
// (a machine key: no project file, no environment).
func machineAtLogin(getenv func(string) string) bool {
	r := config.Resolver{Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	return r.Bool("services.start_at_login")
}

// serviceLogin is `agentbus service login`: the machine's
// services.start_at_login applied now to every project with services
// installed (self-repair step 9), each one's state said. agentbus up applies
// it to its project too; this is for a change made in busmon or config.toml.
func serviceLogin(d serviceCmdDeps, stdout, stderr io.Writer) int {
	on := d.AtLogin != nil && d.AtLogin()
	ls, why := service.LoginStartOf(d.Manager)
	if ls == nil {
		if on {
			fmt.Fprintf(stderr, "agentbus service login: services.start_at_login is on, but %s", why)
			if d.Manager == nil && d.Why != "" {
				fmt.Fprintf(stderr, " (%s)", d.Why)
			}
			fmt.Fprintln(stderr)
			return 1
		}
		fmt.Fprintln(stdout, "services.start_at_login is off: nothing starts at login")
		return 0
	}
	units, err := d.Manager.List()
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	var projects []string
	for _, u := range units {
		if !slices.Contains(projects, u.Project) {
			projects = append(projects, u.Project)
		}
	}
	slices.Sort(projects)
	if len(projects) == 0 {
		fmt.Fprintln(stdout, "no agentbus service installed: agentbus up <project> installs them")
		return 0
	}
	code := 0
	for _, p := range projects {
		was, err := ls.Enabled(p)
		if err != nil && !on { // unknown: disabled anyway, never left as it may be
			was = true
		}
		switch {
		case on:
			if err := ls.Enable(p); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", p, err)
				code = 1
				continue
			}
			fmt.Fprintf(stdout, "%s: starts at login\n", p)
			if err := holderAtLogin(d.Manager, p, d.HolderBuild, true, stdout); err != nil {
				fmt.Fprintf(stderr, "%s: holder: %v\n", p, err)
				code = 1
			}
		case was:
			if err := ls.Disable(p); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", p, err)
				code = 1
				continue
			}
			fmt.Fprintf(stdout, "%s: no longer starts at login\n", p)
			if err := holderAtLogin(d.Manager, p, d.HolderBuild, false, stdout); err != nil {
				fmt.Fprintf(stderr, "%s: holder: %v\n", p, err)
				code = 1
			}
		default:
			fmt.Fprintf(stdout, "%s: does not start at login\n", p)
		}
	}
	return code
}

// holderAtLogin is H1 (holder hardening §3): on, the current build's holder
// unit of project is enabled with the other services and every other build
// disabled (an older build drains, never started again); off, every holder
// of the project is disabled. Nothing is started or stopped. A manager
// without holders, or without a login backend for them, does nothing; on,
// with this build's holder not installed for the project, it is said.
func holderAtLogin(m service.Manager, project string, build func() string, on bool, stdout io.Writer) error {
	hl, ok := m.(service.HolderLoginStart)
	hm, isHM := m.(service.HolderManager)
	if !ok || !isHM {
		return nil
	}
	if !on {
		return hl.DisableHolders(project)
	}
	if build == nil {
		return nil
	}
	raw := build()
	key := service.BuildKey(raw)
	units, err := hm.HolderUnits(project)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(units, func(u service.HolderUnit) bool { return u.Key == key }) {
		fmt.Fprintf(stdout, "%s: no holder of this build (%s) installed: agentbus up %s installs it, then it starts at login too\n", project, raw, project)
		return nil
	}
	if err := hl.EnableHolder(project, key); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "holder: starts at login (build %s; an older build is not started again)\n", raw)
	return nil
}
