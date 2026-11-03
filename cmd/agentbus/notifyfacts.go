package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/notify"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The notifier's facts (notifications spec, "What is notified"): every event
// is a fact the bus already holds and busmon already shows the person. A
// summary line names who and what and the busmon key, never an agent's free
// text, which goes in Detail for full detail to a private target only.

type notifyPlanTask struct{ ID, Title string }

// stallNoticePrefix opens the stall guard's notice for the person
// (stallchecks.go): while team-stall fails, that notice is the stall line.
const stallNoticePrefix = "Stall guard: "

type notifyFacts struct {
	Asks        []bus.Ask
	Agents      map[string]bus.AgentSnapshot
	PlanWaiting []notifyPlanTask
	Health      map[string]health.Record
	Notices     []bus.RawEntry // {p}:notify, retained
	Observed    map[string]bus.Observation
	Paused      bool
	PromptGrace time.Duration // 0 = prompts off, as the desktop ring
}

// notifyEvents is the events that hold now. planSeen is when each waiting
// plan task was first seen (a task carries no since of its own).
func notifyEvents(f notifyFacts, planSeen map[string]time.Time, now time.Time) []notify.Event {
	var out []notify.Event
	add := func(kind, episode string, at time.Time, line, detail string) {
		out = append(out, notify.Event{Kind: kind, Episode: episode, At: at, Line: line, Detail: detail})
	}
	asking := map[string]bool{}
	for _, a := range f.Asks {
		n := a.Open()
		if n == 0 {
			continue
		}
		asking[a.From] = true
		what := "a question"
		if n > 1 {
			what = fmt.Sprintf("%d questions", n)
		}
		add("decision", "ask:"+a.ID, time.UnixMilli(a.CreatedAt), a.From+" asks "+what+" (busmon !)", a.Title)
	}
	for _, name := range sortedAgentNames(f.Agents) {
		s := f.Agents[name]
		if s.State == "waiting" {
			add("decision", "wait:"+name+"|"+strconv.FormatInt(s.TS, 10), time.UnixMilli(s.TS), name+" waits for you (busmon For you)", s.Message)
		}
	}
	for _, t := range f.PlanWaiting {
		at, ok := planSeen[t.ID]
		if !ok {
			at = now
		}
		add("decision", "plan:"+t.ID, at, "plan task "+t.ID+" waits for you (busmon P)", t.Title)
	}
	defs := map[string]health.Definition{}
	for _, d := range health.Definitions() {
		defs[d.ID] = d
	}
	ids := make([]string, 0, len(f.Health))
	for id := range f.Health {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec, def := f.Health[id], defs[id]
		st := health.EffectiveStatus(rec, now.UnixMilli(), def.Interval)
		since := strconv.FormatInt(rec.Since, 10)
		title := def.Title
		if title == "" {
			title = id
		}
		switch {
		case id == "team-stall" && st == health.Fail: // a stall only, never a second fail line
			add("stall", since, time.UnixMilli(rec.Since), "team stall: every agent idle, work ready (busmon F5)", rec.Detail)
		case id == "team-stall":
		case st == health.Fail:
			add("fail", id+"|"+since, time.UnixMilli(rec.Since), title+" fails (busmon F5)", rec.Detail)
		case st == health.Warn && def.PlaceOf(rec, st) == health.PlaceForYou:
			add("for-you", id+"|"+since, time.UnixMilli(rec.Since), title+" needs you (busmon F5)", rec.Detail)
		}
	}
	stalled := false
	for _, e := range out {
		stalled = stalled || e.Kind == "stall"
	}
	for _, e := range f.Notices {
		if e.Fields["for"] != "person" {
			continue
		}
		if stalled && e.Fields["from"] == "dispatch" && strings.HasPrefix(e.Fields["message"], stallNoticePrefix) {
			continue // the stall guard's notice is the same stall: one line (foureyes)
		}
		add("blocked", e.ID, streamTime(e.ID), "a notice only you can act on (busmon Activity)", e.Fields["message"])
	}
	if f.PromptGrace > 0 {
		for _, name := range sortedObsNames(f.Observed) {
			o, s := f.Observed[name], f.Agents[name]
			if o.State != "blocked" || o.PID == 0 || o.PID != s.PID || o.At-o.Since < f.PromptGrace.Milliseconds() ||
				s.State == "blocked" || s.State == "waiting" || asking[name] {
				continue
			}
			add("prompt", fmt.Sprintf("%s|%d:%d", name, o.PID, o.Since), time.UnixMilli(o.Since), name+" waits on a prompt in its pane", "")
		}
	}
	return out
}

func sortedAgentNames(m map[string]bus.AgentSnapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedObsNames(m map[string]bus.Observation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// streamTime is a stream id's time.
func streamTime(id string) time.Time {
	ms, _ := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
	return time.UnixMilli(ms)
}

// verdictCacheMax bounds the verdicts kept in memory, as the ledger is.
const verdictCacheMax = 10000

// notifyReader reads the facts of one project. A source that is absent (no
// plan, no observation, no notice) is an empty set; a read fails only on a
// broker error or a value that does not parse, and names its source.
type notifyReader struct {
	b     *bus.Bus
	grace time.Duration

	mu       sync.Mutex
	verdicts []bus.Verdict
	lastID   string
	planSeen map[string]time.Time
}

func (r *notifyReader) read(ctx context.Context, now time.Time) (notifyFacts, map[string]string) {
	f := notifyFacts{PromptGrace: r.grace}
	failed := map[string]string{}
	var err error
	if f.Asks, err = r.b.Asks(ctx); err != nil {
		failed["asks"] = err.Error()
	}
	if f.Agents, err = r.b.Agents(ctx); err != nil {
		failed["agents"] = err.Error()
	}
	if f.Health, err = r.b.Health(ctx); err != nil {
		failed["health"] = err.Error()
	}
	if f.Observed, err = r.b.Observed(ctx); err != nil {
		failed["observed"] = err.Error()
	}
	if pz, err := r.b.Pause(ctx); err != nil {
		failed["pause"] = err.Error()
	} else {
		f.Paused = pz != nil
	}
	if f.Notices, err = r.notices(ctx); err != nil {
		failed["notices"] = err.Error()
	}
	if f.PlanWaiting, err = r.planWaiting(ctx); err != nil {
		failed["plan"] = err.Error()
	}
	return f, failed
}

// notices reads the retained {p}:notify stream, paged.
func (r *notifyReader) notices(ctx context.Context) ([]bus.RawEntry, error) {
	var out []bus.RawEntry
	after := "0"
	for {
		page, err := r.b.StreamAfter(ctx, "notify", after, 500)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 500 {
			return out, nil
		}
		after = page[len(page)-1].ID
	}
}

// planWaiting is the plan's tasks waiting for the person; none without a
// plan loaded.
func (r *notifyReader) planWaiting(ctx context.Context) ([]notifyPlanTask, error) {
	act, ok, err := plan.Load(ctx, r.b)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	verdicts, err := r.readVerdicts(ctx)
	if err != nil {
		return nil, err
	}
	pin, errs := plan.ReadWithVerdicts(ctx, r.b, act.File, verdicts, true)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	var out []notifyPlanTask
	for _, row := range plan.Derive(act.File, pin) {
		if row.State == plan.Waiting {
			out = append(out, notifyPlanTask{ID: row.Task.ID, Title: row.Task.Title})
		}
	}
	return out, nil
}

func (r *notifyReader) readVerdicts(ctx context.Context) (map[string][]bus.Verdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		vs, err := r.b.VerdictsAfter(ctx, r.lastID, 500)
		if err != nil {
			return nil, err
		}
		r.verdicts = append(r.verdicts, vs...)
		if over := len(r.verdicts) - verdictCacheMax; over > 0 { // the ledger's own cap (foureyes: bounded)
			r.verdicts = append([]bus.Verdict(nil), r.verdicts[over:]...)
		}
		if len(vs) > 0 {
			r.lastID = vs[len(vs)-1].ID
		}
		if len(vs) < 500 {
			return plan.BySubject(r.verdicts, nil), nil
		}
	}
}

// seen keeps when each waiting plan task was first seen, dropping the ones
// no longer waiting (a later wait is a new stretch).
func (r *notifyReader) seen(tasks []notifyPlanTask, now time.Time) map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := map[string]time.Time{}
	for _, t := range tasks {
		if at, ok := r.planSeen[t.ID]; ok {
			next[t.ID] = at
		} else {
			next[t.ID] = now
		}
	}
	r.planSeen = next
	out := make(map[string]time.Time, len(next))
	for k, v := range next {
		out[k] = v
	}
	return out
}
