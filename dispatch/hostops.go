package dispatch

import (
	"context"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The relay's dispatcher side (herdr exit, spec 2026-11-10-herdr-exit-design.md
// §4.2): host directives are read by their own goroutine beside the
// reconciliation, so acceptance never waits on a pass or an operation;
// each one is checked against the closed set, accepted (or expired) by a
// fenced script on Redis TIME that writes its reply in the same step, and
// only then queued on one serial worker that runs it and writes its
// outcome. The worker marks an operation running, under the lease, before
// it runs it, and runs nothing without the lease; a dispatcher starting
// reports each one accepted and never started as not run, and each one
// that was running as unknown (it may have acted: check before asking
// again), never as not run.

// HostOpsPoll is how often the directives are read.
const HostOpsPoll = 250 * time.Millisecond

// hostQueueMax bounds the accepted operations waiting for the worker.
const hostQueueMax = 64

// HostOpsConfig is the relay. Run runs one accepted operation, as an
// outside peer of the holder, under that command's own authorisation with
// op.From as the caller, and returns its outcome (bus.HostDone,
// bus.HostRefused or bus.HostFailed) and a one-line text; nil: the relay is
// off. The operations live with the service (cmd/agentbus).
type HostOpsConfig struct {
	Run  func(ctx context.Context, token string, op bus.HostOp) (state, text string)
	Poll time.Duration // HostOpsPoll when 0
}

func (c HostOpsConfig) on() bool { return c.Run != nil }

// hostOpsState is the reader's: its cursor, whether the unfinished
// operations were reported, and the worker's queue.
type hostOpsState struct {
	cursor string
	swept  bool
	pruned time.Time // the last prune of {p}:hostops (security S10a-4)
	queue  chan bus.HostOp
}

// hostPruneEvery is how often the finished records are pruned.
const hostPruneEvery = time.Hour

// newHostOpsState starts the cursor one window before now (Redis time), so
// a directive written while the dispatcher restarted is still read.
func (l *loop) newHostOpsState(ctx context.Context) *hostOpsState {
	cursor := "0-0"
	if now, err := l.b.ServerTime(ctx); err == nil {
		cursor = strconv.FormatInt(now.Add(-2*bus.HostAcceptWindow).UnixMilli(), 10) + "-0"
	}
	return &hostOpsState{cursor: cursor, queue: make(chan bus.HostOp, hostQueueMax)}
}

// hostOpsLoop reads and accepts on one goroutine, runs on another.
func (l *loop) hostOpsLoop(ctx context.Context) {
	st := l.newHostOpsState(ctx)
	worker := make(chan struct{})
	go func() {
		defer close(worker)
		for ctx.Err() == nil {
			l.hostOpsWork(ctx, st, 1)
		}
	}()
	defer func() { <-worker }() // joined: the loop ends once the worker has
	every := l.cfg.HostOps.Poll
	if every <= 0 {
		every = HostOpsPoll
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.hostOpsPass(ctx, st)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// hostOpsPass, while this dispatcher holds the lease: once, the unfinished
// operations reported as not run; then every new directive refused,
// expired or accepted and queued.
func (l *loop) hostOpsPass(ctx context.Context, st *hostOpsState) {
	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil || !held {
		return
	}
	if !st.swept {
		if err := l.hostOpsSweep(ctx); err != nil {
			l.logOnce("host-ops", map[string]any{"action": "host-ops-failed", "error": err.Error()})
			return
		}
		st.swept = true
	}
	if time.Since(st.pruned) >= hostPruneEvery {
		if _, err := l.b.FencedPruneHostOps(ctx, l.token, bus.HostOpsKeep); err != nil {
			l.logOnce("host-ops", map[string]any{"action": "host-ops-prune-failed", "error": err.Error()})
		} else {
			st.pruned = time.Now()
		}
	}
	ops, last, err := l.b.HostOpsAfter(ctx, st.cursor)
	if err != nil {
		l.logOnce("host-ops", map[string]any{"action": "host-ops-failed", "error": err.Error()})
		return
	}
	st.cursor = last
	for _, o := range ops {
		if err := o.Validate(); err != nil {
			l.hostWrite(o, l.b.FencedHostRefuse(ctx, l.token, o, err.Error()))
			continue
		}
		state, err := l.b.FencedHostAccept(ctx, l.token, o, bus.HostAcceptWindow)
		if err != nil {
			l.hostWrite(o, err)
			continue
		}
		l.log(map[string]any{"agent": o.Agent, "action": "host-" + state, "op": o.Op, "from": o.From, "id": o.ID})
		if state != bus.HostAccepted {
			continue
		}
		l.hostEvent(ctx, o, "accepted")
		select {
		case st.queue <- o:
		default: // never waits: a full queue fails the operation, said
			l.hostWrite(o, l.b.FencedHostOutcome(ctx, l.token, o, bus.HostFailed, "the dispatcher's queue is full"))
		}
	}
}

// hostOpsWork runs n queued operations, in acceptance order, and writes
// each one's outcome.
func (l *loop) hostOpsWork(ctx context.Context, st *hostOpsState, n int) {
	for range n {
		var o bus.HostOp
		select {
		case <-ctx.Done():
			return
		case o = <-st.queue:
		}
		// Under the lease, marked before it runs: without the lease (moved,
		// or ctx ended) it is not run, and stays accepted for the next
		// dispatcher to report as not run.
		if ctx.Err() != nil {
			return
		}
		if err := l.b.FencedHostRunning(ctx, l.token, o); err != nil {
			l.hostWrite(o, err)
			continue
		}
		state, text := l.cfg.HostOps.Run(ctx, l.token, o)
		switch state {
		case bus.HostDone, bus.HostRefused, bus.HostFailed:
		default:
			state, text = bus.HostFailed, "no outcome: "+text
		}
		if text == "" {
			text = o.Op
		}
		l.hostWrite(o, l.b.FencedHostOutcome(ctx, l.token, o, state, text))
		l.hostEvent(ctx, o, state+": "+text)
	}
}

// hostOpsSweep reports each operation with no outcome, to its sender and to
// the escalation target: one never started as not run, one that was running
// as unknown (it may have acted).
func (l *loop) hostOpsSweep(ctx context.Context) error {
	open, err := l.b.UnfinishedHostOps(ctx)
	if err != nil {
		return err
	}
	for _, o := range open {
		state, text, tell := bus.HostNotRun, "the dispatcher stopped before running it", "was not run: the dispatcher stopped before running it; ask again if it is still wanted"
		if o.State == bus.HostRunning {
			state, text, tell = bus.HostUnknown, "the dispatcher stopped while running it: it may have run; check before asking again",
				"may or may not have run: the dispatcher stopped while running it; check before asking again"
		}
		if err := l.b.FencedHostOutcome(ctx, l.token, o, state, text); err != nil {
			return err
		}
		if master := l.cfg.EscalateTo; master != "" && master != o.From {
			what := o.Op
			if o.Agent != "" {
				what += " " + o.Agent
			}
			if _, err := l.b.FencedCmd(ctx, l.token, bus.HostTarget, master, bus.CmdDirective, "host "+what+" from "+o.From+" "+tell); err != nil {
				return err
			}
		}
	}
	return nil
}

// hostEvent records a step of the operation in {p}:events.
func (l *loop) hostEvent(ctx context.Context, o bus.HostOp, detail string) {
	if _, err := l.b.FencedAppendEvent(ctx, l.token, bus.TermEvent{Kind: bus.EventHostOp, Agent: o.Agent, By: o.From,
		Detail: o.Op + " " + detail}); err != nil {
		l.logError(o.Agent, "host event", err)
	}
}

// hostWrite logs a failed relay write.
func (l *loop) hostWrite(o bus.HostOp, err error) {
	if err != nil {
		l.logError(o.Agent, "host "+o.Op, err)
	}
}
