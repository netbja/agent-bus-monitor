package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
	"github.com/netbja/agent-bus-monitor/update"
)

// agentbus update (spec docs/superpowers/specs/2026-11-20-agentbus-update-design.md):
// [--project <p>] update [--check] [--yes] | update --rollback [<id>] [--yes]
// | update --list. --restore <id> is what --rollback runs, with the
// snapshot's own binary.

type updateArgs struct {
	check, yes, list, rollback, restore bool
	auto                                bool // the updater's pass (quiet-update-auto §4.3)
	id                                  string
}

var errUpdateUsage = errors.New("usage: agentbus [--project <p>] update [--check] [--yes] | update --auto [--check] | update --rollback [<id>] [--yes] | update --list")

func parseUpdateArgs(args []string) (updateArgs, error) {
	var a updateArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			a.check = true
		case "--auto":
			a.auto = true
		case "--yes", "-y":
			a.yes = true
		case "--list":
			a.list = true
		case "--rollback", "--restore":
			if args[i] == "--rollback" {
				a.rollback = true
			} else {
				a.restore = true
			}
			if i+1 < len(args) && len(args[i+1]) > 0 && args[i+1][0] != '-' {
				a.id = args[i+1]
				i++
			}
		default:
			return updateArgs{}, errUpdateUsage
		}
	}
	modes := 0
	for _, on := range []bool{a.check, a.list, a.rollback, a.restore} {
		if on {
			modes++
		}
	}
	switch {
	case modes > 1, a.list && a.yes, a.restore && (a.id == "" || a.yes),
		a.auto && (a.yes || a.list || a.rollback || a.restore), // --auto never asks; the setting is its yes
		a.id != "" && (a.id != filepath.Base(a.id) || a.id == "." || a.id == ".."):
		return updateArgs{}, errUpdateUsage
	}
	return a, nil
}

// updateRefusal: updating agentbus is the person's act (§5); --check and
// --list only read.
func updateRefusal(ctx context.Context, a updateArgs, getenv func(string) string, ancestor func(context.Context) (string, error)) string {
	if a.check || a.list {
		return ""
	}
	return agentcaller.Refuse(ctx, getenv, ancestor, "updating agentbus", "agentbus update")
}

func runUpdate(ctx context.Context, args []string, project, host string, out, errw io.Writer) int {
	a, err := parseUpdateArgs(args)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	if why := updateRefusal(ctx, a, os.Getenv, agentAncestor); why != "" {
		fmt.Fprintln(errw, why)
		return 1
	}
	if buildinfo.Bin == "" {
		fmt.Fprintln(errw, "agentbus update: this agentbus names no bin dir (not built by setup): update with ./setup.sh update from your clone")
		return 1
	}
	d := realUpdateDeps(host, out, errw)
	d.Acted = func(a update.Act) { // its audit line on each project it touched (audit step 2b)
		auditHostConnect(ctx, host, bus.AuditEntry{Action: "update", Actor: personActor(os.Getenv), Target: a.To, Outcome: a.Outcome, Detail: a.Detail}, a.Projects)
	}
	switch {
	case a.list:
		return update.List(d)
	case a.restore:
		return update.Restore(ctx, d, a.id)
	case a.rollback:
		return update.RollbackTo(ctx, d, a.id, a.yes)
	case a.auto:
		return autoPass(a.check)(ctx, d)
	}
	return update.Run(ctx, d, update.Options{Project: project, Check: a.check, Yes: a.yes})
}

// autoPass is update --auto's pass (quiet-update-auto §4.3): --check reads
// only; without it the pass installs when updates.auto = install (the
// person's yes given ahead), else it checks.
func autoPass(check bool) func(context.Context, update.Deps) int {
	if check {
		return update.CheckPass
	}
	return update.InstallPass
}

func realUpdateDeps(host string, out, errw io.Writer) update.Deps {
	d := update.Deps{R: execRunner{}, Getenv: os.Getenv, Environ: os.Environ(), Now: time.Now, Sleep: time.Sleep,
		Out: out, Err: errw, Version: buildinfo.Version, Repo: buildinfo.Repo, BinDir: filepath.Dir(buildinfo.Bin),
		FromBuild: service.BuildKey(holder.BuildID()), Settle: 60 * time.Second,
		Running: func(context.Context) ([]update.Service, error) { return runningServices(realServiceCmdDeps(host)) }}
	sd := realServiceCmdDeps(host)
	d.HolderKeys = func(context.Context) (map[string]bool, error) { return holderKeys(sd) }
	d.HolderGC = func(_ context.Context, current string) error { return holderGC(sd, current) }
	d.Holders = func(context.Context) ([]string, error) { return activeHolderProjects(sd) }
	d.Judge = func(ctx context.Context, p string) (update.Judged, error) { return judgeProject(ctx, sd, p) }
	d.AutoSetting = func() string { return updatesAuto(os.Getenv) }
	d.MaxWait = func() time.Duration { return updatesMaxWait(os.Getenv) }
	d.Notify = func(ctx context.Context, n update.Notice) error { return updateNotify(ctx, sd.Bus, os.Getenv, n) }
	if tty := setup.OpenTTY(); tty != nil {
		d.TTY = tty
	}
	return d
}

// runningServices are this host's active dispatch and missions units, each
// with the build it recorded: staleServices against no build at all lists
// every one (holders are not units, so never listed).
func runningServices(d serviceCmdDeps) ([]update.Service, error) {
	units, err := d.Manager.List()
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	state := func(p, k string) service.State { st, _ := d.Manager.State(p, k); return st }
	records := func(p string) (map[string]bus.ServiceRecord, error) {
		b, err := d.Bus(p)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, recs, err := b.ServiceStates(ctx)
		return recs, err
	}
	var out []update.Service
	for _, s := range staleServices(units, state, records, host, "\x00no build") {
		out = append(out, update.Service{Project: s.Project, Kind: s.Kind, Build: s.Build})
	}
	return out, nil
}

// holderKeys are the holder builds on disk (quiet-update-auto §5, rule 3):
// every project's holder units, and every binary copy under the holder
// directory (supervised holders, under services.manager = none, have no
// unit). An unreadable directory or unit list is an error: the prune then
// keeps every tree.
func holderKeys(d serviceCmdDeps) (map[string]bool, error) {
	keys := map[string]bool{}
	if hm, ok := d.Manager.(service.HolderManager); ok {
		units, err := hm.HolderUnits("")
		if err != nil {
			return nil, err
		}
		for _, u := range units {
			keys[u.Key] = true
		}
	}
	ents, err := os.ReadDir(filepath.Dir(service.HolderBinDir(d.Paths, "x")))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range ents {
		if e.IsDir() && service.ValidBuildKey(e.Name()) {
			keys[e.Name()] = true
		}
	}
	return keys, nil
}

// holderGC is service gc's holder collection for every project with a holder
// unit, then the binary copies no unit records, current the new build: a
// holder that runs or answers on its socket is never collected (spec 4.4a).
func holderGC(d serviceCmdDeps, current string) error {
	hm, ok := d.Manager.(service.HolderManager)
	if !ok {
		return nil
	}
	units, err := hm.HolderUnits("")
	if err != nil {
		return err
	}
	var errs []error
	seen := map[string]bool{}
	for _, u := range units {
		if seen[u.Project] {
			continue
		}
		seen[u.Project] = true
		p := u.Project
		answers := func(key string) bool { return d.HolderAnswers != nil && d.HolderAnswers(p, key) }
		if _, err := service.GCHolders(hm, d.Paths, p, current, answers); err != nil {
			errs = append(errs, err)
		}
	}
	any := func(key string) bool { return d.HolderAnswersAny != nil && d.HolderAnswersAny(key) }
	if _, err := service.GCHolderBinaries(hm, d.Paths, current, any); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// activeHolderProjects are the projects with an active holder of any build
// (quiet-update-auto §4.3 step 4): every holder unit, or under
// services.manager = none every supervised holder, judged by the manager. A
// record it cannot read, or a manager that cannot list strictly or say
// whether one runs, is an error: the pass is deferred (reviewer-codex on 4a).
func activeHolderProjects(d serviceCmdDeps) ([]string, error) {
	hm, ok := d.Manager.(service.HolderManager)
	if !ok {
		return nil, nil // no holders on this manager
	}
	sl, ok := d.Manager.(service.StrictHolderLister)
	if !ok {
		return nil, errors.New("this service manager cannot list its holders strictly")
	}
	units, err := sl.HolderUnitsStrict("")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range units {
		active, err := hm.HolderActive(u.Project, u.Key)
		if err != nil {
			return nil, fmt.Errorf("holder %s of %s: %w", u.Key, u.Project, err)
		}
		if active && !slices.Contains(out, u.Project) {
			out = append(out, u.Project)
		}
	}
	return out, nil
}

// judgeProject reads project's agents and its conductor's missions, at its
// broker's TIME, without a token (quiet-update-auto §4.3 step 4).
func judgeProject(ctx context.Context, d serviceCmdDeps, project string) (update.Judged, error) {
	b, err := d.Bus(project)
	if err != nil {
		return update.Judged{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now, err := b.ServerTime(ctx)
	if err != nil {
		return update.Judged{}, err
	}
	agents, err := b.Agents(ctx)
	if err != nil {
		return update.Judged{}, err
	}
	j := update.Judged{Now: now}
	for name, a := range agents {
		f := update.AgentFact{Name: name, State: a.State}
		if a.TS > 0 {
			f.Changed = time.UnixMilli(a.TS)
		}
		j.Agents = append(j.Agents, f)
	}
	s := mission.Store{B: b}
	changes, err := s.Changes(ctx)
	if err != nil {
		return update.Judged{}, err
	}
	for _, c := range changes {
		ms, err := s.Missions(ctx, c)
		if err != nil {
			return update.Judged{}, err
		}
		for id, m := range ms {
			j.Missions = append(j.Missions, update.MissionFact{Change: c, ID: id, State: string(m.State)})
		}
	}
	return j, nil
}

// updatesAuto is updates.auto from config.toml (no env variable): notify,
// unless the person wrote install.
func updatesAuto(getenv func(string) string) string {
	f := config.Load(config.Path(getenv), "config.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{f}}
	return r.String("updates.auto")
}

// updatesMaxWait is updates.max_wait from config.toml (no env variable).
func updatesMaxWait(getenv func(string) string) time.Duration {
	f := config.Load(config.Path(getenv), "config.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{f}}
	return r.Duration("updates.max_wait")
}

// updaterAgent is the sender of the automatic update's notices, a service
// identity like dispatch and conductor (quiet-update-auto §4.6).
const updaterAgent = "updater"

// updateNotify sends n to each of its projects' escalation targets (the
// project's AGENT_BUS_ESCALATE_TO, master by default, as the resume
// directive reads it): a report from updater naming it, then its notice,
// waking it only when n says so. Every project is tried; the errors are
// joined (a broker down loses that project's notice).
func updateNotify(ctx context.Context, busFor func(string) (*bus.Bus, error), getenv func(string) string, n update.Notice) error {
	var errs []error
	for _, p := range n.Projects {
		if err := updateNotifyOne(ctx, busFor, getenv, p, n); err != nil {
			errs = append(errs, fmt.Errorf("project %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

func updateNotifyOne(ctx context.Context, busFor func(string) (*bus.Bus, error), getenv func(string) string, p string, n update.Notice) error {
	b, err := busFor(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	to := suspendNotify(getenv, p)
	id, err := b.ReportTo(ctx, updaterAgent, bus.ReportNote, n.Text, []string{to})
	if err != nil {
		return err
	}
	if n.Wake {
		_, err = b.Cmd(ctx, updaterAgent, to, bus.CmdDirective, id, reportNotice(id, n.Text))
	} else {
		_, err = b.CmdNoWake(ctx, updaterAgent, to, bus.CmdDirective, id, reportNotice(id, n.Text))
	}
	return err
}
