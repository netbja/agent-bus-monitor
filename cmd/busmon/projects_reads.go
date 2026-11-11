package main

import (
	"context"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// recordingReader is a project's reader for the Projects page's counts: it
// passes every read through and keeps the first that failed. The poll keeps
// a failed read's last value, or none; for a count read once, none would be
// a 0, so any failed read makes the counts unknown (?), reviewer-codex.
type recordingReader struct {
	b   busReader
	mu  sync.Mutex
	bad error
}

func (r *recordingReader) note(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bad == nil {
		r.bad = err
	}
}

func (r *recordingReader) failed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bad
}

func rec1[T any](r *recordingReader, v T, err error) (T, error) { r.note(err); return v, err }

func (r *recordingReader) PilotDriver(ctx context.Context) (string, error) {
	v, err := r.b.PilotDriver(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) ArmedAgents(ctx context.Context) (map[string]string, error) {
	v, err := r.b.ArmedAgents(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) CmdLag(ctx context.Context) (map[string]int64, error) {
	v, err := r.b.CmdLag(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error) {
	v, err := r.b.Agents(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Usage(ctx context.Context) (map[string]bus.UsageSnapshot, error) {
	v, err := r.b.Usage(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Budgets(ctx context.Context) (map[string]bus.BudgetSnapshot, error) {
	v, err := r.b.Budgets(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Board(ctx context.Context) (map[string]bus.BoardEntry, error) {
	v, err := r.b.Board(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Recent(ctx context.Context, kinds []string, n int) ([]bus.Event, map[string]string, error) {
	ev, cur, err := r.b.Recent(ctx, kinds, n)
	r.note(err)
	return ev, cur, err
}
func (r *recordingReader) DispatchAlive(ctx context.Context) (bool, error) {
	v, err := r.b.DispatchAlive(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) ServiceStates(ctx context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error) {
	inst, recs, err := r.b.ServiceStates(ctx)
	r.note(err)
	return inst, recs, err
}
func (r *recordingReader) ConductorAlive(ctx context.Context) (bool, error) {
	v, err := r.b.ConductorAlive(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) ChangeNames(ctx context.Context) ([]string, error) {
	v, err := r.b.ChangeNames(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Obligation(ctx context.Context, agent string, horizon time.Duration) (bus.Obligation, error) {
	v, err := r.b.Obligation(ctx, agent, horizon)
	return rec1(r, v, err)
}
func (r *recordingReader) Wake(ctx context.Context, agent string) (bus.WakeRecord, bool, error) {
	rec, ok, err := r.b.Wake(ctx, agent)
	r.note(err)
	return rec, ok, err
}
func (r *recordingReader) OpenChallenges(ctx context.Context, agent string) (map[string]string, error) {
	v, err := r.b.OpenChallenges(ctx, agent)
	return rec1(r, v, err)
}
func (r *recordingReader) Pause(ctx context.Context) (*bus.PauseRecord, error) {
	v, err := r.b.Pause(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) QuotaMarks(ctx context.Context) (map[string]string, error) {
	v, err := r.b.QuotaMarks(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Observed(ctx context.Context) (map[string]bus.Observation, error) {
	v, err := r.b.Observed(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Context(ctx context.Context) (map[string]bus.ContextFill, error) {
	v, err := r.b.Context(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) SwapIntents(ctx context.Context) (map[string]bus.SwapIntent, error) {
	v, err := r.b.SwapIntents(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Handoffs(ctx context.Context) (map[string]bus.Handoff, error) {
	v, err := r.b.Handoffs(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Fallbacks(ctx context.Context) (map[string]bus.FallbackRecord, error) {
	v, err := r.b.Fallbacks(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) ModelVerdicts(ctx context.Context) (map[string]bus.ModelVerdict, error) {
	v, err := r.b.ModelVerdicts(ctx)
	return rec1(r, v, err)
}
func (r *recordingReader) Asks(ctx context.Context) ([]bus.Ask, error) {
	v, err := r.b.Asks(ctx)
	return rec1(r, v, err)
}

// recordingFull is the recording reader of a reader that also has the
// plan, the merge queue and the health checks (*bus.Bus): For you's merge
// and check rows, and the plan's decide and NEXT rows, come from them.
type recordingFull struct {
	*recordingReader
	p planReader
	m mergeQueueReader
	g guardianReader
}

func (r recordingFull) Plan(ctx context.Context) (bus.PlanRecord, bool, error) {
	rec, has, err := r.p.Plan(ctx)
	r.note(err)
	return rec, has, err
}
func (r recordingFull) PlanFacts(ctx context.Context) (map[string]bus.PlanFact, error) {
	v, err := r.p.PlanFacts(ctx)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) VerdictsAfter(ctx context.Context, after string, count int64) ([]bus.Verdict, error) {
	v, err := r.p.VerdictsAfter(ctx, after, count)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) ReadMissionHash(ctx context.Context, key string) (map[string]string, error) {
	v, err := r.p.ReadMissionHash(ctx, key)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) MergeQueue(ctx context.Context) (bus.MergeQueueRecord, error) {
	v, err := r.m.MergeQueue(ctx)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) Health(ctx context.Context) (map[string]guardian.Record, error) {
	v, err := r.g.Health(ctx)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) HealthNotes(ctx context.Context) (map[string]guardian.Note, error) {
	v, err := r.g.HealthNotes(ctx)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) ServerTime(ctx context.Context) (time.Time, error) {
	v, err := r.g.ServerTime(ctx)
	return rec1(r.recordingReader, v, err)
}
func (r recordingFull) Project() string {
	if p, ok := r.b.(interface{ Project() string }); ok {
		return p.Project()
	}
	return ""
}

// recording wraps b: the full form when b has every reader the counts use,
// else the plain one (a test's fake: no plan, merge queue or health then).
func recording(b busReader) (busReader, func() error) {
	r := &recordingReader{b: b}
	p, okP := b.(planReader)
	m, okM := b.(mergeQueueReader)
	g, okG := b.(guardianReader)
	if okP && okM && okG {
		return recordingFull{recordingReader: r, p: p, m: m, g: g}, r.failed
	}
	return r, r.failed
}
