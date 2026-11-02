package main

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
)

// popDoneDeps is what pop-done reads, injected (pop-auto-retire §7).
type popDoneDeps struct {
	Agents func(context.Context) (map[string]bus.AgentSnapshot, error)
	Marks  func(context.Context) (map[string]string, error)
	Now    func(context.Context) (int64, error) // Redis TIME, ms
	Getenv func(string) string
}

func realPopDoneDeps(b *bus.Bus) popDoneDeps {
	return popDoneDeps{Agents: b.Agents, Marks: b.QuotaMarks, Now: b.ServerNowMS, Getenv: os.Getenv}
}

// popDoneCheck reads every registered pop, its declaration time, and whether
// the dispatcher's mark names that episode, for health.EvaluatePopDone.
func popDoneCheck(d popDoneDeps) func(context.Context) (health.Outcome, error) {
	return func(ctx context.Context) (health.Outcome, error) {
		r := config.Machine(d.Getenv)
		after := r.Duration("dispatch.pop_retire_after")
		if !r.Bool("dispatch.pop_retire") || after == 0 {
			return health.EvaluatePopDone(health.PopDoneInput{Off: true}), nil
		}
		snaps, err := d.Agents(ctx)
		if err != nil {
			return health.Outcome{Status: health.Unknown, Detail: "agent registry unavailable"}, err
		}
		marks, err := d.Marks(ctx)
		if err != nil {
			return health.Outcome{Status: health.Unknown, Detail: "dispatcher marks unavailable"}, err
		}
		now, err := d.Now(ctx)
		if err != nil {
			return health.Outcome{Status: health.Unknown, Detail: "broker time unavailable"}, err
		}
		in := health.PopDoneInput{Now: time.UnixMilli(now), After: after}
		for name, s := range snaps {
			if s.Tier != "pop" {
				continue
			}
			in.Pops = append(in.Pops, health.PopDoneAgent{Name: name, Since: time.UnixMilli(s.TS),
				Told: marks[bus.PopDoneMark(name)] == strconv.FormatInt(s.TS, 10)})
		}
		return health.EvaluatePopDone(in), nil
	}
}
