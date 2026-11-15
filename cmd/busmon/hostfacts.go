package main

import (
	"context"
	"os"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// What the agent row says of an agent's terminal on a holder (herdr exit spec
// 4.6, step 7b2): blocked on its client's prompt (4.3a, the dispatcher's
// blocked/unblocked events), a draft left unsent in its input (4.5, the
// holder's Activity), and the last action background work made the
// dispatcher skip (4.3b, its background-skip events). busmon only reads.

// hostFacts is one agent's; the zero value says nothing.
type hostFacts struct {
	blocked    bool
	blockedWhy string // the prompt, as the dispatcher quoted it
	blockedAt  time.Time
	draft      bool
	draftSince time.Time
	skip       string // "auto-clear skipped — 2 checks running"
	skipAt     time.Time
}

// hostSkipShown is how long a background skip stays on the row.
const hostSkipShown = 10 * time.Minute

// hostEventsRead is how many of the newest events are read for it.
const hostEventsRead = 500

// hostFactsEvery is the watcher's period.
const hostFactsEvery = 2 * time.Second

type eventsReader interface {
	RecentEvents(ctx context.Context, n int64) ([]bus.TermEvent, error)
}

// activityReader is the part of termhost.Host the draft needs: reads only.
type activityReader interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Activity(ctx context.Context, id termhost.TermID) (termhost.Activity, error)
}

// readHostFacts reads the events (newest first) and, with a host, each
// agent's newest live terminal's draft. A failed events read is the error; a
// holder that fails only loses the drafts.
func readHostFacts(ctx context.Context, evs eventsReader, terms activityReader, project string, now time.Time) (map[string]hostFacts, error) {
	out := map[string]hostFacts{}
	if evs != nil {
		list, err := evs.RecentEvents(ctx, hostEventsRead)
		if err != nil {
			return nil, err
		}
		seenBlock, seenSkip := map[string]bool{}, map[string]bool{}
		for _, e := range list {
			if e.Agent == "" {
				continue
			}
			f := out[e.Agent]
			switch e.Kind {
			case bus.EventBlocked, bus.EventUnblocked:
				if seenBlock[e.Agent] {
					continue
				}
				seenBlock[e.Agent] = true
				if e.Kind == bus.EventBlocked {
					f.blocked, f.blockedWhy, f.blockedAt = true, e.Detail, e.At
				}
			case bus.EventBackgroundSkip:
				if seenSkip[e.Agent] {
					continue
				}
				seenSkip[e.Agent] = true
				if now.Sub(e.At) <= hostSkipShown {
					f.skip, f.skipAt = e.Detail, e.At
				}
			default:
				continue
			}
			out[e.Agent] = f
		}
	}
	if terms == nil {
		return out, nil
	}
	list, err := terms.List(ctx, project)
	if err != nil {
		return out, nil
	}
	done := map[string]bool{}
	for i := len(list) - 1; i >= 0; i-- { // the newest first
		t := list[i]
		if t.Project != project || !t.Alive || t.Name == "" || done[t.Name] {
			continue
		}
		done[t.Name] = true
		a, err := terms.Activity(ctx, t.ID)
		if err != nil || !a.Draft {
			continue
		}
		f := out[t.Name]
		f.draft, f.draftSince = true, a.DraftSince
		out[t.Name] = f
	}
	return out, nil
}

// applyHostFacts writes the facts on the agents busmon knows; an agent left
// out has none.
func applyHostFacts(st *shared, facts map[string]hostFacts) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for n, a := range st.agents {
		if a != nil {
			a.host = facts[n]
		}
	}
}

// busmonActivity is the project's holders for the drafts; nil when the
// project cannot have one.
func busmonActivity(project string) activityReader {
	h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
	if err != nil {
		return nil
	}
	return h
}

// watchHostFacts keeps the facts current until ctx ends; a failed read keeps
// the last ones.
func watchHostFacts(ctx context.Context, st *shared, evs eventsReader, terms activityReader, project string) {
	t := time.NewTicker(hostFactsEvery)
	defer t.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, hostFactsEvery)
		facts, err := readHostFacts(c, evs, terms, project, time.Now())
		cancel()
		if err == nil {
			applyHostFacts(st, facts)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
