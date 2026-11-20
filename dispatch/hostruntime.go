package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// HostRuntime is the dispatcher's Runtime on a terminal host (herdr exit
// step 9, spec docs/superpowers/specs/2026-11-10-herdr-exit-design.md 4.2,
// 4.3, 4.3b): termhost.Host gives the facts, agentstate judges them. A pane
// is a termhost.TermID. Nothing in the running system uses it yet: the
// services' dispatcher runs on it since the cutover (step 10).
//
// What changes from herdr's: the client comes from the agent's launch
// profile (Client), never from the screen; a screen is settled when the
// program has written nothing for Settle (not two equal reads); Focused is
// "a person is active" (see below); the
// identity is the host's own child and its tree; and each look also carries
// the blocked observation (4.3a) and the background judgement (4.3b).
//
// A person active is a draft, a key within PersonQuiet, or a non-observer
// viewer attached and not proven unfocused (Activity.FocusedViewers, 4.5):
// the wake then waits under the focus rule, never attempted.
type HostRuntime struct {
	Host    termhost.Host
	Project string
	// Client is an agent's client by its launch profile (roles.toml), "" when
	// unknown; a client without an agentstate profile is unsupported.
	Client func(agent string) string
	// Hooks is what an agent's hooks last said of its background work, for
	// its process pid (a report of another process is not its); false (or nil
	// Hooks): not read, which no client whose hooks are authoritative ever
	// judges as "none".
	Hooks func(agent string, pid int) (agentstate.HookFacts, bool)
	// ExtraHelpers: the MCP servers the launcher configured for an agent.
	ExtraHelpers func(agent string) []string

	PersonQuiet time.Duration // dispatch.person_quiet; 0: DefaultPersonQuiet
	Settle      time.Duration // output quiet for a settled screen; 0: DefaultSettle
	Window      time.Duration // the CPU window of the helpers' activity; 0: DefaultCPUWindow
	Now         func() time.Time

	mu      sync.Mutex
	samples map[termhost.TermID][]cpuSample
}

// The host path's defaults (spec 4.3 input 4, 4.3b, 4.5).
const (
	DefaultSettle      = 800 * time.Millisecond
	DefaultPersonQuiet = 3 * time.Second // dispatch.person_quiet's default
	DefaultCPUWindow   = 10 * time.Second
)

var _ Runtime = (*HostRuntime)(nil)

// procKey is one process, its pid reused never confused with another.
type procKey struct {
	pid   int
	start uint64
}

// cpuSample is a terminal's processes' CPU time at one look.
type cpuSample struct {
	at  time.Time
	cpu map[procKey]time.Duration
}

func (r *HostRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// find is the terminal named by pane among the project's.
func (r *HostRuntime) find(ctx context.Context, pane string) (termhost.Term, error) {
	terms, err := r.Host.List(ctx, r.Project)
	if err != nil {
		return termhost.Term{}, err
	}
	for _, t := range terms {
		if string(t.ID) == pane {
			return t, nil
		}
	}
	return termhost.Term{}, termhost.ErrUnknownTerm
}

// hostFailed records a failed look: an unknown terminal is gone.
func hostFailed(in Inspection, err error) Inspection {
	if errors.Is(err, termhost.ErrUnknownTerm) {
		in.Gone = true
	} else {
		in.Err = err
	}
	in.Ready = false
	return in
}

// Inspect reports the terminal's facts and agentstate's judgement of them.
func (r *HostRuntime) Inspect(ctx context.Context, pane string) Inspection {
	in := Inspection{State: "unknown"}
	t, err := r.find(ctx, pane)
	if err == nil && !t.Alive {
		err = termhost.ErrUnknownTerm // its program ended: nothing to wake there
	}
	if err != nil {
		if errors.Is(err, termhost.ErrUnknownTerm) {
			r.forget(termhost.TermID(pane))
		}
		return hostFailed(in, err)
	}
	id := t.ID
	if r.Client != nil {
		in.Client = r.Client(t.Name)
	}
	procs, err := r.Host.Processes(ctx, id)
	if err != nil {
		return hostFailed(in, err)
	}
	now := r.now()
	r.identity(&in, t, procs)

	before, err := r.Host.Activity(ctx, id)
	if err != nil {
		return hostFailed(in, err)
	}
	in.Focused = before.PersonActive(now, orDefault(r.PersonQuiet, DefaultPersonQuiet))
	prof, supported := agentstate.Profiles()[in.Client]
	in.Background = r.background(t, prof.Background, procs, now)
	sc, err := r.Host.Screen(ctx, id, 40)
	if err != nil {
		return hostFailed(in, err)
	}
	after, err := r.Host.Activity(ctx, id)
	if err != nil {
		return hostFailed(in, err)
	}
	// Settled: nothing written for Settle, and nothing while the screen was read.
	settled := after.OutputAt.Equal(before.OutputAt) &&
		(after.OutputAt.IsZero() || now.Sub(after.OutputAt) >= orDefault(r.Settle, DefaultSettle))
	in.Blocked = &agentstate.Observation{PID: t.PID, At: now, Settled: settled}
	if !supported {
		return in // unsupported: never ready
	}
	sig := prof.Signature
	lines := sc.Lines
	if sig.ANSI {
		lines = sc.Styled
	}
	text := strings.Join(lines, "\n")
	ready, busy := sig.Match(text)
	in.Busy = busy
	in.Ready = settled && ready
	if settled {
		in.Blocked.Screen, in.Blocked.What = sig.BlockedOn(text)
		sum := sha256.Sum256([]byte(text))
		in.ScreenHash = hex.EncodeToString(sum[:12])
	}
	switch {
	case in.Blocked.Screen, !ready && sig.InDialog(text):
		in.State, in.Ready = "blocked", false
	case busy:
		in.State = "working"
	case ready:
		in.State = "idle"
	}
	return in
}

// forget drops a gone terminal's CPU samples (foureyes: a long-lived
// dispatcher outlives many terminals).
func (r *HostRuntime) forget(id termhost.TermID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.samples, id)
}

// identity: the host's child and every process of its tree, with their start
// times (the verified pid+start rule) and their agent environment.
func (r *HostRuntime) identity(in *Inspection, t termhost.Term, procs []termhost.Proc) {
	in.Starts, in.Env = map[int]uint64{}, map[int]string{}
	add := func(pid int, start uint64) {
		if _, seen := in.Starts[pid]; seen || pid <= 0 {
			return
		}
		in.PIDs = append(in.PIDs, pid)
		in.Starts[pid] = start
		if who := procAgent(pid); who != "" {
			in.Env[pid] = who
		}
	}
	add(t.PID, t.PIDStart)
	for _, p := range procs {
		add(p.PID, p.Start)
	}
}

// background judges the terminal's background work (4.3b): its descendants
// beside the host's child, their CPU growth over the window from an earlier
// look (none that old yet: the process facts are not read), the hooks'
// facts, and the profile's running-tasks screen.
func (r *HostRuntime) background(t termhost.Term, bg agentstate.Background, procs []termhost.Proc, now time.Time) *agentstate.Judgement {
	window := orDefault(r.Window, DefaultCPUWindow)
	cur := cpuSample{at: now, cpu: map[procKey]time.Duration{}}
	for _, p := range procs {
		cur.cpu[procKey{p.PID, p.Start}] = p.CPU
	}
	r.mu.Lock()
	if r.samples == nil {
		r.samples = map[termhost.TermID][]cpuSample{}
	}
	hist := r.samples[t.ID]
	var base *cpuSample
	for i := range hist { // the newest look at least a window old
		if now.Sub(hist[i].at) >= window {
			base = &hist[i]
		}
	}
	var kept []cpuSample
	for _, s := range hist {
		if now.Sub(s.at) < 3*window && (base == nil || !s.at.Before(base.at)) {
			kept = append(kept, s)
		}
	}
	r.samples[t.ID] = append(kept, cur)
	r.mu.Unlock()

	var f agentstate.Facts
	if base != nil {
		pf := &agentstate.ProcessFacts{Window: now.Sub(base.at)}
		for _, p := range procs {
			if p.PID == t.PID {
				continue // the agent itself, never its own background
			}
			k := procKey{p.PID, p.Start}
			grown := p.CPU - base.cpu[k] // a process new since the base: all its CPU
			pf.Procs = append(pf.Procs, agentstate.Proc{Name: p.Command, CPUGrowth: grown})
		}
		f.Process = pf
	}
	if r.Hooks != nil {
		if h, ok := r.Hooks(t.Name, t.PID); ok {
			f.Hooks = &h
		}
	}
	if r.ExtraHelpers != nil {
		f.ExtraHelpers = r.ExtraHelpers(t.Name)
	}
	j := bg.Judge(f, now)
	return &j
}

// Prompt submits a wake line; observed means the host saw it land and sent
// CR. A refusal (a person active, not ready, a line left behind) types
// nothing; ErrNotLanded left the line unsent in the input.
func (r *HostRuntime) Prompt(ctx context.Context, pane string, line termhost.Line) (bool, error) {
	if line.Kind() != termhost.KindWake {
		return false, errors.New("prompt: not a wake line")
	}
	if err := r.Host.Submit(ctx, termhost.TermID(pane), line); err != nil {
		return false, err
	}
	return true, nil
}

// Clear submits /clear (termhost.ClearLine), the one other line.
func (r *HostRuntime) Clear(ctx context.Context, pane string) error {
	return r.Host.Submit(ctx, termhost.TermID(pane), termhost.ClearLine())
}

// Ping reports whether the host answers.
func (r *HostRuntime) Ping(ctx context.Context) error { return r.Host.Ping(ctx) }
