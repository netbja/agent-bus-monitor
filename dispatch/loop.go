package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ErrAlreadyRunning: another dispatcher holds the project's lease.
var ErrAlreadyRunning = errors.New("dispatch already running")

// ErrLeaseLost: this dispatcher no longer holds the lease (it lapsed, another
// took it, or the project was deleted). It has stopped writing.
var ErrLeaseLost = errors.New("dispatch lease lost")

// dispatchFrom is the sender of every escalation and notify the loop writes.
const dispatchFrom = "dispatch"

// Config holds the loop's tunables; DefaultConfig has the pinned values.
type Config struct {
	Horizon, Retry, EscalateAfter, Tick, Lease, Renew, MinPTTL time.Duration
	MaxTries                                                   int
	EscalateTo                                                 string
	Log                                                        io.Writer // one JSON line per decision
	// OnLease, when set, is told when this dispatcher takes the lease (true)
	// and when Run gives it up (false): agentbus service run reports "ready".
	OnLease func(held bool)
	// Quota, when its Read is set, collects provider readings and holds the
	// agents of a provider near its limit (v0.13).
	Quota QuotaConfig
}

// DefaultConfig returns the spec's pinned values.
func DefaultConfig() Config {
	return Config{
		Horizon:       bus.DefaultHorizon,
		Retry:         60 * time.Second,
		EscalateAfter: 10 * time.Minute,
		Tick:          5 * time.Second,
		Lease:         15 * time.Second,
		Renew:         5 * time.Second,
		MinPTTL:       5 * time.Second,
		MaxTries:      3,
		EscalateTo:    "master",
		Log:           os.Stderr,
	}
}

// filled replaces zero fields with defaults and rejects unusable combinations.
func (c Config) filled() (Config, error) {
	d := DefaultConfig()
	for _, f := range []struct{ v, def *time.Duration }{
		{&c.Horizon, &d.Horizon}, {&c.Retry, &d.Retry}, {&c.EscalateAfter, &d.EscalateAfter},
		{&c.Tick, &d.Tick}, {&c.Lease, &d.Lease}, {&c.Renew, &d.Renew}, {&c.MinPTTL, &d.MinPTTL},
	} {
		if *f.v <= 0 {
			*f.v = *f.def
		}
	}
	if c.MaxTries <= 0 {
		c.MaxTries = d.MaxTries
	}
	if c.EscalateTo == "" {
		c.EscalateTo = d.EscalateTo
	}
	if c.Log == nil {
		c.Log = d.Log
	}
	if c.Quota.Read != nil {
		c.Quota = c.Quota.filled()
	}
	if !bus.ValidName(c.EscalateTo) {
		return c, fmt.Errorf("invalid escalation recipient %q", c.EscalateTo)
	}
	if c.Renew >= c.Lease || c.MinPTTL >= c.Lease {
		return c, fmt.Errorf("renew (%v) and min PTTL (%v) must be shorter than the lease (%v)", c.Renew, c.MinPTTL, c.Lease)
	}
	return c, nil
}

// WakeText is the exact line typed into an agent's pane. It carries no message
// body: peer content is never typed into a terminal (spec §3.3).
func WakeText(project, agent, nonce string, n int64) string {
	return fmt.Sprintf("[agentbus#%s] %s: %d message(s) waiting for %s. Run: agentbus --project %s subscribe --drain %s",
		nonce, project, n, agent, project, agent)
}

// hooks are crash-injection points for tests; production leaves them nil.
type hooks struct {
	afterReserve         func() // the reservation is written, the prompt not sent
	afterPrompt          func() // the prompt returned, its outcome not recorded
	afterEscalatePending func() // escalated=pending is written, nothing sent
}

// testHooks is read once when Run starts; only tests set it.
var testHooks hooks

func call(f func()) {
	if f != nil {
		f()
	}
}

// Error backoff: 1 s doubling to 30 s (spec global constraints).
var backoffMin, backoffMax = time.Second, 30 * time.Second

func randomHex(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error()) // never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// Run holds the project's dispatch lease and reconciles every agent until ctx
// ends (nil), the lease is lost (ErrLeaseLost) or another dispatcher already
// runs (ErrAlreadyRunning). It keeps no durable local state: every decision is
// recomputed from Redis and the runtime, so a restart resumes where the last
// run stopped (spec §3).
func Run(ctx context.Context, b *bus.Bus, rt Runtime, cfg Config) error {
	if b == nil || rt == nil {
		return errors.New("dispatch: nil bus or runtime")
	}
	cfg, err := cfg.filled()
	if err != nil {
		return err
	}
	token := randomHex(8)
	ok, err := b.AcquireDispatch(ctx, token, cfg.Lease)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAlreadyRunning
	}
	l := &loop{b: b, rt: rt, cfg: cfg, token: token, h: testHooks, last: map[string]string{},
		policy: Policy{Retry: cfg.Retry, EscalateAfter: cfg.EscalateAfter, MaxTries: cfg.MaxTries, Supported: Supported()}}

	inner, cancel := context.WithCancel(ctx)
	var lost atomic.Bool
	var wg sync.WaitGroup
	if cfg.OnLease != nil {
		cfg.OnLease(true)
	}
	defer func() {
		cancel()
		wg.Wait()
		rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer rcancel()
		_ = b.ReleaseDispatch(rctx, token) // CAS: a no-op when the lease is gone
		if cfg.OnLease != nil {
			cfg.OnLease(false)
		}
	}()

	wg.Add(2)
	go func() { // lease renewal; losing it stops the loop
		defer wg.Done()
		t := time.NewTicker(cfg.Renew)
		defer t.Stop()
		for {
			select {
			case <-inner.Done():
				return
			case <-t.C:
			}
			held, err := b.RenewDispatch(inner, token, cfg.Lease)
			if inner.Err() != nil {
				return
			}
			if err != nil {
				l.logError("", "renew lease", err)
				continue // a lease that truly lapses fails the next renew
			}
			if !held {
				lost.Store(true)
				cancel()
				return
			}
		}
	}()

	signal := make(chan struct{}, 1)
	go func() { // XREAD BLOCK on {p}:cmd from "$": a latency signal only
		defer wg.Done()
		for inner.Err() == nil {
			err := b.TailFrom(inner, map[string]string{bus.StreamKey(b.Project(), "cmd"): "$"}, []string{"cmd"},
				func(bus.Event) {
					select {
					case signal <- struct{}{}:
					default:
					}
				})
			if inner.Err() != nil {
				return
			}
			l.logError("", "tail cmd", err)
			select {
			case <-inner.Done():
				return
			case <-time.After(backoffMin):
			}
		}
	}()

	var backoff time.Duration
	for {
		err := l.reconcile(inner)
		switch {
		case lost.Load(), errors.Is(err, bus.ErrNotHolder), errors.Is(err, ErrLeaseLost):
			return ErrLeaseLost
		case ctx.Err() != nil:
			return nil
		}
		wait := cfg.Tick
		if err != nil {
			l.logError("", "reconcile", err)
			backoff = min(max(2*backoff, backoffMin), backoffMax)
			wait = backoff
		} else {
			backoff = 0
		}
		timer := time.NewTimer(wait)
		sig := signal
		if err != nil {
			sig = nil // an error backs off; new entries do not cut it short
		}
		select {
		case <-inner.Done():
		case <-sig:
		case <-timer.C:
		}
		timer.Stop()
		if lost.Load() {
			return ErrLeaseLost
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

type loop struct {
	b      *bus.Bus
	rt     Runtime
	cfg    Config
	token  string
	policy Policy
	h      hooks

	logMu sync.Mutex
	last  map[string]string // agent → last logged decision, so a steady state logs once

	quota quotaState
}

// reconcile visits every agent the bus knows of: registered (snapshot), with a
// consumer group, or addressed by a retained cmd entry. A lease error ends the
// pass at once; any other error is logged and the pass goes on to the next
// agent, returning the first error so Run backs off.
func (l *loop) reconcile(ctx context.Context) error {
	snaps, err := l.b.Agents(ctx)
	if err != nil {
		return err
	}
	// Quota first: it may set or end a quota pause, and its holds must apply
	// even when the pause cannot be read (paused() fails open).
	if err := l.quotaPass(ctx, snaps); err != nil {
		if errors.Is(err, bus.ErrNotHolder) {
			return err
		}
		l.logError("", "quota", err)
	}
	if paused, err := l.paused(ctx); err != nil || paused {
		return err
	}
	groups, err := l.b.GroupNames(ctx)
	if err != nil {
		return err
	}
	targets, err := l.b.CmdTargets(ctx)
	if err != nil {
		return err
	}
	hasGroup, isTarget := map[string]bool{}, map[string]bool{}
	names := map[string]bool{}
	for a := range snaps {
		names[a] = true
	}
	for _, g := range groups {
		hasGroup[g], names[g] = true, true
	}
	for _, t := range targets {
		isTarget[t], names[t] = true, true
	}
	sorted := make([]string, 0, len(names))
	for a := range names {
		if bus.ValidName(a) {
			sorted = append(sorted, a)
		}
	}
	sort.Strings(sorted)

	var first error
	for _, a := range sorted {
		var snap *bus.AgentSnapshot
		if s, ok := snaps[a]; ok {
			snap = &s
		}
		if l.heldBy(snap) { // its provider is at its limit: no prompt, no escalation
			l.logOnce(a, map[string]any{"agent": a, "action": "none", "reason": "quota held: " + snap.Provider})
			continue
		}
		err := l.agent(ctx, a, snap, hasGroup[a], isTarget[a])
		if err == nil {
			continue
		}
		if errors.Is(err, bus.ErrNotHolder) || errors.Is(err, ErrLeaseLost) || ctx.Err() != nil {
			return err
		}
		l.logError(a, "reconcile agent", err)
		if first == nil {
			first = err
		}
	}
	return first
}

// step is one agent's state for this pass.
type step struct {
	agent  string
	snap   *bus.AgentSnapshot
	o      bus.Obligation
	w      bus.WakeRecord // stored record (zero when none)
	exists bool
	x      Input
}

func (l *loop) agent(ctx context.Context, agent string, snap *bus.AgentSnapshot, hasGroup, isTarget bool) error {
	if !hasGroup && isTarget {
		if err := l.b.FencedEnsureGroup(ctx, l.token, agent, l.cfg.Horizon); err != nil {
			return err
		}
	}
	o, err := l.b.Obligation(ctx, agent, l.cfg.Horizon)
	if err != nil {
		return err
	}
	w, exists, err := l.b.Wake(ctx, agent)
	if err != nil {
		return err
	}
	if !o.Wakes() && !exists && !o.GapPossible {
		return nil // nothing owed, nothing recorded: no writes, no log
	}
	armed, err := l.b.Armed(ctx, agent)
	if err != nil {
		return err
	}
	now, err := l.b.ServerTime(ctx)
	if err != nil {
		return err
	}
	s := &step{agent: agent, snap: snap, o: o, w: w, exists: exists}
	s.x = Input{Obligation: o, Armed: armed, Snap: snap}
	if exists {
		s.x.Wake = &s.w
		if w.At != 0 {
			s.x.SinceWake = time.Duration(now.UnixMilli()-w.At) * time.Millisecond
		}
	}
	if ms, _, ok := parseID(o.OldestID); ok && o.OldestID != "" {
		s.x.OldestAge = time.Duration(now.UnixMilli()-int64(ms)) * time.Millisecond
	}

	// Rules 1–6 never look at the pane; inspect only when Decide gets past them.
	d := Decide(l.policy, s.x)
	if d.Rule >= 7 {
		s.x.In = l.inspect(ctx, snap)
		d = Decide(l.policy, s.x)
	}
	if err := l.act(ctx, s, d); err != nil {
		return err
	}
	return l.gap(ctx, s)
}

func (l *loop) inspect(ctx context.Context, snap *bus.AgentSnapshot) Inspection {
	if snap == nil || snap.Pane == "" {
		return Inspection{Err: errors.New("no herdr pane registered")}
	}
	return l.rt.Inspect(ctx, snap.Pane)
}

// put writes rec as the agent's record and remembers it for the rest of the pass.
func (l *loop) put(ctx context.Context, s *step, rec bus.WakeRecord) error {
	if err := l.b.PutWake(ctx, s.agent, l.token, rec); err != nil {
		return err
	}
	s.w, s.exists = rec, true
	return nil
}

// base is the stored record with a reset applied (controller ruling 2): on
// progress or identity change, tries and escalated clear and the progress
// marks and identity are taken from now, so the write that follows becomes
// the new baseline.
func (s *step) base(d Decision) bus.WakeRecord {
	r := s.w
	if d.ResetTries {
		r.Tries, r.Escalated = 0, ""
		r.LastDelivered, r.PendingTotal = s.o.LastDelivered, s.o.PendingTotal
		r.PID, r.PIDStart = 0, 0
		if s.snap != nil {
			r.PID, r.PIDStart = s.snap.PID, s.snap.PIDStart
		}
	}
	return r
}

func (l *loop) act(ctx context.Context, s *step, d Decision) error {
	switch d.Action {
	case ActNone:
		l.logDecision(s, d)
		if d.Clear && s.exists {
			return l.clear(ctx, s)
		}
		return nil
	case ActDefer:
		l.logDecision(s, d)
		return l.deferTo(ctx, s, d)
	case ActEscalate:
		return l.escalate(ctx, s, d)
	case ActPrompt:
		return l.prompt(ctx, s, d)
	}
	return fmt.Errorf("unknown action %v", d.Action)
}

// clear is rule 1 (controller ruling 1): never delete the record — that would
// erase the retry window — but reset it, keeping At, the identity and the gap
// flag. Written only when it is not already in that state.
func (l *loop) clear(ctx context.Context, s *step) error {
	r := s.w
	r.State, r.Tries, r.Escalated, r.Reason, r.HighID = "clear", 0, "", "", ""
	r.LastDelivered, r.PendingTotal = s.o.LastDelivered, s.o.PendingTotal
	if r == s.w {
		return nil
	}
	return l.put(ctx, s, r)
}

// deferTo keeps At, tries, the progress marks and escalated (ruling 3) and
// records why; an unchanged record is not rewritten.
func (l *loop) deferTo(ctx context.Context, s *step, d Decision) error {
	r := s.base(d)
	r.State, r.Reason = "deferred", d.Reason
	if s.exists && r == s.w {
		return nil
	}
	return l.put(ctx, s, r)
}

// prompt is spec §3.3: re-check, reserve (fenced, server-windowed), confirm
// the lease, prompt, record the outcome.
func (l *loop) prompt(ctx context.Context, s *step, d Decision) error {
	// Step 0: readiness and focus can change between the inspection and now.
	s.x.In = l.inspect(ctx, s.snap)
	if d2 := Decide(l.policy, s.x); d2.Action != ActPrompt {
		return l.act(ctx, s, d2)
	}

	next := s.base(d)
	next.State, next.Reason, next.HighID = "prompting", d.Reason, s.o.HighID
	next.Tries = s.w.Tries + 1
	if d.ResetTries {
		next.Tries = 1
	}
	next.LastDelivered, next.PendingTotal = s.o.LastDelivered, s.o.PendingTotal
	next.PID, next.PIDStart = s.snap.PID, s.snap.PIDStart
	stored, reserved, err := l.b.ReserveWake(ctx, s.agent, l.token, next, l.cfg.Retry, l.cfg.MinPTTL)
	if err != nil {
		return err
	}
	if !reserved {
		// Ruling 4: the server refused (window still open, or too little lease
		// left): no prompt; defer on the stored record.
		s.w, s.exists = stored, stored != (bus.WakeRecord{})
		dd := Decision{Action: ActDefer, Rule: 5, Reason: "retry window", ResetTries: d.ResetTries}
		l.logDecision(s, dd)
		return l.deferTo(ctx, s, dd)
	}
	s.w, s.exists = stored, true
	call(l.h.afterReserve)

	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil {
		return err
	}
	if !held {
		return ErrLeaseLost
	}
	nonce := randomHex(3)
	observed, perr := l.rt.Prompt(ctx, s.snap.Pane, WakeText(l.b.Project(), s.agent, nonce, s.o.Size()), nonce)
	call(l.h.afterPrompt)
	if ctx.Err() != nil {
		return ctx.Err() // stopping: the reservation already bounds a retry
	}
	l.logDecision(s, d)
	r := stored
	r.State = "prompted"
	if !observed || perr != nil {
		r.State = "unobserved"
	}
	if perr != nil {
		r.Reason = "prompt error: " + perr.Error()
		l.logError(s.agent, "prompt", perr)
	}
	return l.put(ctx, s, r)
}

// escalate is spec §3.4: escalated=pending (fenced) → escalation cmd to the
// recipient role (skipped when the stuck agent is the recipient) + notify →
// escalated=true. A crash in between resends once on restart (Decide rule 3).
func (l *loop) escalate(ctx context.Context, s *step, d Decision) error {
	l.logDecision(s, d)
	r := s.base(d)
	if r.LastDelivered == "" && r.PendingTotal == 0 {
		// No reservation ever set a baseline: take one now, so progress after
		// the escalation can be told apart from none (rule 3).
		r.LastDelivered, r.PendingTotal = s.o.LastDelivered, s.o.PendingTotal
	}
	if r.State == "" {
		r.State = "deferred"
	}
	r.Escalated, r.Reason = "pending", d.Reason
	if !s.exists || r != s.w {
		if err := l.put(ctx, s, r); err != nil {
			return err
		}
	}
	call(l.h.afterEscalatePending)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	body := fmt.Sprintf("%s: %s; %d waiting, oldest %s", s.agent, d.Reason, s.o.Size(), s.o.OldestID)
	note := body
	if s.agent == l.cfg.EscalateTo {
		note = "⚠ " + body + " (the escalation recipient itself is stuck)"
	} else if _, err := l.b.FencedCmd(ctx, l.token, dispatchFrom, l.cfg.EscalateTo, bus.CmdEscalation, body); err != nil {
		return err
	}
	if _, err := l.b.FencedNotify(ctx, l.token, dispatchFrom, note); err != nil {
		return err
	}
	r.Escalated = "true"
	return l.put(ctx, s, r)
}

// gap sends the one notify of a possible trim-before-read (spec §6) and
// persists the flag in the wake record.
func (l *loop) gap(ctx context.Context, s *step) error {
	if !s.o.GapPossible || s.w.GapNotified {
		return nil
	}
	msg := fmt.Sprintf("%s: entries may have been trimmed from the cmd stream before %s read them (group cursor %s)",
		s.agent, s.agent, s.o.LastDelivered)
	if _, err := l.b.FencedNotify(ctx, l.token, dispatchFrom, msg); err != nil {
		return err
	}
	r := s.w
	if !s.exists {
		r.State, r.LastDelivered, r.PendingTotal = "clear", s.o.LastDelivered, s.o.PendingTotal
	}
	r.GapNotified = true
	l.log(map[string]any{"agent": s.agent, "action": "notify", "reason": "gap possible", "high_id": s.o.HighID})
	return l.put(ctx, s, r)
}

// logDecision writes one JSON line per decision; a decision identical to the
// agent's previous one is not repeated (prompts and escalations always are).
func (l *loop) logDecision(s *step, d Decision) {
	key := fmt.Sprintf("%v|%d|%s", d.Action, d.Rule, d.Reason)
	l.logMu.Lock()
	same := l.last[s.agent] == key
	l.last[s.agent] = key
	l.logMu.Unlock()
	if same && d.Action != ActPrompt && d.Action != ActEscalate {
		return
	}
	var latency int64
	if s.o.OldestID != "" {
		latency = s.x.OldestAge.Milliseconds()
	}
	l.log(map[string]any{"agent": s.agent, "action": d.Action.String(), "rule": d.Rule,
		"reason": d.Reason, "high_id": s.o.HighID, "latency_ms": latency})
}

func (l *loop) logError(agent, op string, err error) {
	l.log(map[string]any{"agent": agent, "op": op, "error": err.Error()})
}

func (l *loop) log(v map[string]any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	l.logMu.Lock()
	defer l.logMu.Unlock()
	_, _ = l.cfg.Log.Write(append(line, '\n'))
}

// paused handles a project pause: while it runs nobody is woken or escalated
// (the pass stops here); once due, one fenced script ends it and sends the
// resume directive, and the pass goes on, so that directive wakes its target.
func (l *loop) paused(ctx context.Context) (bool, error) {
	p, err := l.b.Pause(ctx)
	if err != nil || p == nil {
		if err != nil {
			l.logError("", "read pause", err)
		}
		return false, nil
	}
	id, err := l.b.FencedResume(ctx, l.token, dispatchFrom)
	switch {
	case errors.Is(err, bus.ErrNotHolder):
		return true, err
	case err != nil:
		l.logError("", "resume", err)
		return true, nil
	case id != "":
		l.log(map[string]any{"action": "resume", "notify": p.Notify, "id": id})
		l.logMu.Lock()
		delete(l.last, "")
		l.logMu.Unlock()
		return false, nil
	}
	key := fmt.Sprintf("paused|%d", p.Until)
	l.logMu.Lock()
	same := l.last[""] == key
	l.last[""] = key
	l.logMu.Unlock()
	if !same {
		l.log(map[string]any{"action": "paused", "until": p.UntilTime().Format(time.RFC3339), "reason": p.Reason})
	}
	return true, nil
}
