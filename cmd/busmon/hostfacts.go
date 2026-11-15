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

// hostEventsRead is how many of the newest events each read takes; the
// first read, and a read that finds the previous one's newest entry gone
// (more than this many appended between two reads), takes the whole stream.
const (
	hostEventsRead = 500
	hostEventsAll  = 10000 // bus eventsMaxLen: the stream's cap
)

// hostFactsEvery is the watcher's period.
const hostFactsEvery = 2 * time.Second

// eventsReader is the bus part the facts need: the timeline and the broker's
// clock (the events' times are the broker's; a skip's age is judged on it).
type eventsReader interface {
	RecentEvents(ctx context.Context, n int64) ([]bus.TermEvent, error)
	ServerTime(ctx context.Context) (time.Time, error)
}

// activityReader is the part of termhost.Host the draft needs: reads only.
type activityReader interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Activity(ctx context.Context, id termhost.TermID) (termhost.Activity, error)
}

// eventState is one agent's latest blocked/unblocked and skip events as the
// watcher last saw them: kept until a newer event of the agent replaces it,
// so an agent stays blocked however many other events follow (the stream is
// a timeline, read through a window).
type eventState struct {
	blocked    bool
	blockedWhy string
	blockedAt  time.Time
	skip       string
	skipAt     time.Time
	blockID    string // the event that set it ("" none seen)
	skipID     string
}

// hostWatcher is the watcher's memory between reads.
type hostWatcher struct {
	agents map[string]eventState
	newest string // the newest event id of the last read ("" before the first)
}

// events reads the timeline into w.agents: the newest window, or the whole
// stream on the first read and after a gap.
func (w *hostWatcher) events(ctx context.Context, evs eventsReader) error {
	n := int64(hostEventsRead)
	if w.newest == "" {
		n = hostEventsAll
	}
	list, err := evs.RecentEvents(ctx, n)
	if err != nil {
		return err
	}
	if w.newest != "" && int64(len(list)) == n && idAfter(list[len(list)-1].ID, w.newest) {
		if list, err = evs.RecentEvents(ctx, hostEventsAll); err != nil { // a gap: the window missed entries
			return err
		}
	}
	if w.agents == nil {
		w.agents = map[string]eventState{}
	}
	for _, e := range list { // newest first: an agent's first entry of a kind is its latest
		if e.Agent == "" {
			continue
		}
		st := w.agents[e.Agent]
		switch e.Kind {
		case bus.EventBlocked, bus.EventUnblocked:
			if st.blockID != "" && !idAfter(e.ID, st.blockID) {
				continue
			}
			st.blockID = e.ID
			st.blocked, st.blockedWhy, st.blockedAt = e.Kind == bus.EventBlocked, "", time.Time{}
			if st.blocked {
				st.blockedWhy, st.blockedAt = e.Detail, e.At
			}
		case bus.EventBackgroundSkip:
			if st.skipID != "" && !idAfter(e.ID, st.skipID) {
				continue
			}
			st.skipID, st.skip, st.skipAt = e.ID, e.Detail, e.At
		default:
			continue
		}
		w.agents[e.Agent] = st
	}
	if len(list) > 0 && idAfter(list[0].ID, w.newest) {
		w.newest = list[0].ID
	}
	return nil
}

// read is one pass: the events, the broker's clock, and with a host the
// draft of each agent's terminal that attach picks (holderTerm). A failed
// events or clock read is the error (the last facts stay); a holder that
// fails only loses the drafts.
func (w *hostWatcher) read(ctx context.Context, evs eventsReader, terms activityReader, project string) (map[string]hostFacts, error) {
	if err := w.events(ctx, evs); err != nil {
		return nil, err
	}
	now, err := evs.ServerTime(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]hostFacts{}
	for name, st := range w.agents {
		f := hostFacts{blocked: st.blocked, blockedWhy: st.blockedWhy, blockedAt: st.blockedAt}
		if st.skip != "" && now.Sub(st.skipAt) <= hostSkipShown {
			f.skip, f.skipAt = st.skip, st.skipAt
		}
		out[name] = f
	}
	if terms == nil {
		return out, nil
	}
	list, err := terms.List(ctx, project)
	if err != nil {
		return out, nil
	}
	names := map[string]bool{}
	for _, t := range list {
		names[t.Name] = true
	}
	for name := range names {
		t, ok := holderTerm(list, project, name) // the terminal attach picks
		if !ok {
			continue
		}
		a, err := terms.Activity(ctx, t.ID)
		if err != nil || !a.Draft {
			continue
		}
		f := out[name]
		f.draft, f.draftSince = true, a.DraftSince
		out[name] = f
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
	var w hostWatcher
	for {
		c, cancel := context.WithTimeout(ctx, hostFactsEvery)
		facts, err := w.read(c, evs, terms, project)
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
