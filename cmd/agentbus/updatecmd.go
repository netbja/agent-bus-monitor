package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
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
	id                                  string
}

var errUpdateUsage = errors.New("usage: agentbus [--project <p>] update [--check] [--yes] | update --rollback [<id>] [--yes] | update --list")

func parseUpdateArgs(args []string) (updateArgs, error) {
	var a updateArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			a.check = true
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
	switch {
	case a.list:
		return update.List(d)
	case a.restore:
		return update.Restore(ctx, d, a.id)
	case a.rollback:
		return update.RollbackTo(ctx, d, a.id, a.yes)
	}
	return update.Run(ctx, d, update.Options{Project: project, Check: a.check, Yes: a.yes})
}

func realUpdateDeps(host string, out, errw io.Writer) update.Deps {
	d := update.Deps{R: execRunner{}, Getenv: os.Getenv, Environ: os.Environ(), Now: time.Now, Sleep: time.Sleep,
		Out: out, Err: errw, Version: buildinfo.Version, Repo: buildinfo.Repo, BinDir: filepath.Dir(buildinfo.Bin),
		FromBuild: service.BuildKey(holder.BuildID()), Settle: 60 * time.Second,
		Running: func(context.Context) ([]update.Service, error) { return runningServices(realServiceCmdDeps(host)) }}
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
