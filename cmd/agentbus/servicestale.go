package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// staleService is a project service running another build than this
// agentbus (P2-13): Build is the one it recorded, "" when unknown (started
// before builds were recorded, or its record is another process's).
type staleService struct{ Project, Kind, Build string }

// staleServices are units, running on this host, whose process recorded a
// build other than build. A stopped unit starts on the binary installed now;
// a record from another host says nothing about this one. Holders are not
// units (m.List): an upgrade never restarts one (herdr exit spec 4.4a).
func staleServices(units []service.Unit, state func(project, kind string) service.State,
	records func(project string) (map[string]bus.ServiceRecord, error), host, build string) []staleService {
	var out []staleService
	read := map[string]map[string]bus.ServiceRecord{}
	for _, u := range units {
		st := state(u.Project, u.Kind)
		if !st.Active {
			continue
		}
		recs, ok := read[u.Project]
		if !ok {
			recs, _ = records(u.Project) // unreadable: no record, so unknown, so listed
			read[u.Project] = recs
		}
		r, ok := recs[u.Kind]
		switch {
		case ok && r.Host != "" && r.Host != host:
			continue
		case !ok || (st.PID > 0 && r.PID != st.PID):
			out = append(out, staleService{u.Project, u.Kind, ""})
		case r.Build != build:
			out = append(out, staleService{u.Project, u.Kind, r.Build})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// printStale is one "<project> <kind> <build>" line each, "-" for an
// unknown build: what setup reads.
func printStale(w io.Writer, list []staleService) {
	for _, s := range list {
		b := s.Build
		if b == "" {
			b = "-"
		}
		fmt.Fprintf(w, "%s %s %s\n", s.Project, s.Kind, b)
	}
}

// serviceStale is `agentbus service stale`: this host's project services
// still running another build than this agentbus, for setup's question.
func serviceStale(d serviceCmdDeps, stdout, stderr io.Writer) int {
	units, err := d.Manager.List()
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	host, _ := os.Hostname()
	build := ""
	if d.HolderBuild != nil {
		build = d.HolderBuild()
	}
	state := func(p, k string) service.State { st, _ := d.Manager.State(p, k); return st }
	records := func(p string) (map[string]bus.ServiceRecord, error) {
		if d.Bus == nil {
			return nil, nil
		}
		b, err := d.Bus(p)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, recs, err := b.ServiceStates(ctx)
		return recs, err
	}
	printStale(stdout, staleServices(units, state, records, host, build))
	return 0
}
