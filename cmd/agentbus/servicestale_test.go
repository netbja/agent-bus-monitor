package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// P2-13: a service records the build it runs, so an install can tell which
// project services still run the previous one.
func TestServiceRecordsItsBuild(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.Build = "v9.9.9.build"
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-ctx.Done()
		return 0
	}
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	if r := waitState(t, b, service.Dispatch, "ready"); r.Build != "v9.9.9.build" {
		t.Fatalf("record %+v", r)
	}
}

// P2-13 (seen 2026-11-16: ai-tradex-solana, hyperliquid and demo kept the old
// binary): service stale lists this host's running dispatch and missions
// services whose recorded build is not this agentbus's, a record from before
// builds were recorded included; never a holder, never a stopped unit,
// never another host's record or a record of another process.
func TestStaleServices(t *testing.T) {
	units := []service.Unit{
		{Project: "alpha", Kind: service.Dispatch}, // this build
		{Project: "beta", Kind: service.Dispatch},  // an older build
		{Project: "beta", Kind: service.Missions},  // no build recorded: older than builds being recorded
		{Project: "gamma", Kind: service.Dispatch}, // stopped: it starts on the new binary
		{Project: "delta", Kind: service.Dispatch}, // its record is another process's
		{Project: "eps", Kind: service.Dispatch},   // its record is another host's
	}
	state := map[string]service.State{
		"alpha/dispatch": {Active: true, PID: 10}, "beta/dispatch": {Active: true, PID: 11}, "beta/missions": {Active: true, PID: 12},
		"gamma/dispatch": {Active: false}, "delta/dispatch": {Active: true, PID: 14}, "eps/dispatch": {Active: true, PID: 15},
	}
	recs := map[string]map[string]bus.ServiceRecord{
		"alpha": {service.Dispatch: {State: "running", PID: 10, Host: "h1", Build: "new"}},
		"beta":  {service.Dispatch: {State: "running", PID: 11, Host: "h1", Build: "old"}, service.Missions: {State: "running", PID: 12, Host: "h1"}},
		"gamma": {service.Dispatch: {State: "stopped", Host: "h1", Build: "old"}},
		"delta": {service.Dispatch: {State: "running", PID: 99, Host: "h1", Build: "new"}},
		"eps":   {service.Dispatch: {State: "running", PID: 15, Host: "h2", Build: "old"}},
	}
	got := staleServices(units, func(p, k string) service.State { return state[p+"/"+k] },
		func(p string) (map[string]bus.ServiceRecord, error) { return recs[p], nil }, "h1", "new")
	want := []staleService{
		{Project: "beta", Kind: service.Dispatch, Build: "old"},
		{Project: "beta", Kind: service.Missions, Build: ""},
		{Project: "delta", Kind: service.Dispatch, Build: ""}, // the record names another pid: its build is unknown
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	var out bytes.Buffer
	printStale(&out, got)
	if s := out.String(); s != "beta dispatch old\nbeta missions -\ndelta dispatch -\n" {
		t.Fatalf("output %q", s)
	}
}
