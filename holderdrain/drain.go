// Package holderdrain is `agentbus holder drain` (herdr exit spec 4.4).
package holderdrain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// An older build's holder never ends by itself while its agents live (/clear
// ends no terminal), so the person finishes the drain: each terminal still on
// an old build is moved to the current one, one agent at a time, by the
// reload protocol (agentbus reload) on the holder: the reload intent first,
// so the dispatcher neither wakes nor clears the agent; a wake being typed
// waited out; the same idle process re-checked; the old terminal closed and
// its program gone; then the new terminal started with the agent's resume
// session, so two clients never run one session; the proof; the ledger's
// reloaded entry. An agent working, blocked, with a person active or a
// draft, or with background work (4.3b, unknown counts as running) is
// skipped; the command says so and is run again later. Never automatic.

// Ledger is the bus part the drain uses; *bus.Bus is one.
type Ledger interface {
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	ResumeOf(ctx context.Context, agent string) (*bus.ResumeRecord, error)
	BeginSwap(ctx context.Context, role string, in bus.SwapIntent, ev ...bus.SwapEvent) error
	EndSwap(ctx context.Context, role string) error
	LogSwap(ctx context.Context, e bus.SwapEvent) error
	FinishReload(ctx context.Context, e bus.SwapEvent) error
	Wake(ctx context.Context, agent string) (bus.WakeRecord, bool, error)
	AppendEvent(ctx context.Context, e bus.TermEvent) (string, error)
}

// Deps is what the drain reads and does; NewDeps wires production's.
type Deps struct {
	Project string
	Host    termhost.Host
	Bus     Ledger
	// Old: the terminal is on a build other than the current one.
	Old func(termhost.Term) bool
	// Sockets is every holder socket of the project on disk: the drain acts
	// only when each one answers (Describe), so no terminal is unseen.
	Sockets func() ([]string, error)
	// Inspect is the dispatcher's look at a terminal (HostRuntime.Inspect):
	// its state, a person active, its background work.
	Inspect func(ctx context.Context, t termhost.Term) dispatch.Inspection
	GC      func() error
	Sleep   func(time.Duration)
	Now     func() time.Time
	// Window is the background judgement's CPU window: the old terminals are
	// looked at once, then again a window later.
	Window                         time.Duration
	WakeWait, ProofWait, ProofPoll time.Duration
	// IgnoreBackground is the person's --ignore-background (spec 4.3b, as
	// for relaunch, swap and suspend): a client with no authoritative
	// source (Codex, Grok, Kimi, agy) is never judged free of background
	// work, so without it the person could never drain those. Working, a
	// person active and a draft still skip.
	IgnoreBackground bool
}

// drainBy is the author of the drain's ledger entries and events.
const drainBy = "holder-drain"

// Exit codes: 0 drained, 1 failed, 3 agents skipped (run it again later).
const Skipped = 3

// errSkip is an agent left for a later run, with why.
type errSkip struct{ why string }

func (e errSkip) Error() string { return e.why }

// inventory refuses a drain while a holder socket of the project does not
// answer: its terminals would be unseen, and the drain would call the
// project drained with agents still on it.
func inventory(ctx context.Context, d Deps) error {
	files, err := d.Sockets()
	if err != nil {
		return fmt.Errorf("the holder sockets: %w", err)
	}
	info, err := d.Host.Describe(ctx)
	if err != nil {
		return fmt.Errorf("the holders: %w", err)
	}
	var silent []string
	for _, f := range files {
		if !slices.Contains(info.Sockets, f) {
			silent = append(silent, f)
		}
	}
	if len(silent) > 0 {
		sort.Strings(silent)
		return fmt.Errorf("holder socket(s) that do not answer: %v; their terminals cannot be seen, so nothing is done", silent)
	}
	return nil
}

// oldTerms is the project's live terminals on an old build, by agent.
func oldTerms(ctx context.Context, d Deps) ([]termhost.Term, error) {
	terms, err := d.Host.List(ctx, d.Project)
	if err != nil {
		return nil, err
	}
	var out []termhost.Term
	for _, t := range terms {
		if t.Alive && t.Project == d.Project && d.Old(t) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// inTerm: snapshot s's verified process is t's program or in its tree.
func inTerm(ctx context.Context, h termhost.Host, s bus.AgentSnapshot, t termhost.Term) (bool, error) {
	if s.PID <= 0 || s.PIDStart == 0 {
		return false, nil
	}
	if s.PID == t.PID && s.PIDStart == t.PIDStart {
		return true, nil
	}
	procs, err := h.Processes(ctx, t.ID)
	if err != nil {
		return false, err
	}
	for _, p := range procs {
		if p.PID == s.PID && p.Start == s.PIDStart {
			return true, nil
		}
	}
	return false, nil
}

// skipWhy is why the agent's terminal is not moved now, or "".
func skipWhy(ctx context.Context, d Deps, t termhost.Term, s bus.AgentSnapshot) string {
	if s.State == "working" {
		return "it declares working"
	}
	in := d.Inspect(ctx, t)
	switch {
	case in.Err != nil || in.Gone:
		return "its terminal could not be read"
	case in.State == "working" || in.State == "blocked":
		return "its terminal shows it " + in.State
	case in.Focused:
		return "a person is active in its terminal"
	case d.IgnoreBackground:
	case in.Background == nil:
		return "its background work is unknown (--ignore-background to move it anyway)"
	case in.BackgroundSkip() != "":
		return in.BackgroundSkip() + " (--ignore-background to move it anyway)"
	}
	a, err := d.Host.Activity(ctx, t.ID)
	if err != nil {
		return "its terminal could not be read"
	}
	if a.Draft {
		return "a line is typed in its input, not sent"
	}
	return ""
}

// move runs the reload protocol for old's agent; an errSkip leaves it.
func move(ctx context.Context, d Deps, old termhost.Term) error {
	agent := old.Name
	snaps, err := d.Bus.Agents(ctx)
	if err != nil {
		return fmt.Errorf("the agents: %w", err)
	}
	snap, ok := snaps[agent]
	if in, err := inTerm(ctx, d.Host, snap, old); !ok || err != nil || !in {
		return errSkip{"its proved process is not this terminal's (agentbus up relaunches an agent the bus does not know)"}
	}
	if why := skipWhy(ctx, d, old, snap); why != "" {
		return errSkip{why}
	}
	rec, err := d.Bus.ResumeOf(ctx, agent)
	if err != nil {
		return fmt.Errorf("its resume record: %w", err)
	}
	spec := termhost.Spec{Project: d.Project, Name: agent}
	if rec != nil {
		spec.Profile, spec.ResumeSession = rec.Profile, rec.Session
	}
	ev := bus.SwapEvent{Kind: bus.SwapKindReload, FromProfile: spec.Profile, ToProfile: spec.Profile, By: drainBy,
		Reason: "holder drain", OldPID: snap.PID, OldPane: snap.Pane}
	err = d.Bus.BeginSwap(ctx, agent, bus.SwapIntent{Kind: bus.SwapKindReload, ToProfile: spec.Profile, By: drainBy,
		OldPID: snap.PID, OldStart: snap.PIDStart}, ev)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return errSkip{"a swap or reload of it is in progress"}
	}
	if err != nil {
		return fmt.Errorf("its reload intent: %w", err)
	}
	defer func() { _ = d.Bus.EndSwap(context.Background(), agent) }()
	failed := func(step string, err error) error {
		_ = d.Bus.LogSwap(context.Background(), bus.SwapEvent{Role: agent, Event: bus.SwapFailed, Kind: bus.SwapKindReload,
			ToProfile: spec.Profile, By: drainBy, Error: step + ": " + err.Error()})
		return err
	}
	// A wake reserved before the intent may still be typing: let it land in
	// the old terminal rather than in a closing one.
	for deadline := d.Now().Add(d.WakeWait); ; d.Sleep(200 * time.Millisecond) {
		w, ok, err := d.Bus.Wake(ctx, agent)
		if err != nil {
			return failed("wake record", fmt.Errorf("cannot read its wake record (%v); nothing was closed", err))
		}
		if !ok || w.State != "prompting" {
			break
		}
		if !d.Now().Before(deadline) {
			_ = failed("wake in progress", errors.New("a wake is still being typed"))
			return errSkip{"a wake is being typed into its terminal"}
		}
	}
	// The same idle process as checked, or nothing is closed.
	if now, err := d.Bus.Agents(ctx); err != nil {
		return failed("stop", fmt.Errorf("%v; nothing was closed", err))
	} else if s := now[agent]; s.PID != snap.PID || s.PIDStart != snap.PIDStart {
		_ = failed("stop", errors.New("its process changed since the check"))
		return errSkip{"its process changed since the check"}
	} else if why := skipWhy(ctx, d, old, s); why != "" {
		_ = failed("stop", errors.New(why))
		return errSkip{why}
	}
	if err := d.Host.Close(ctx, old.ID); err != nil {
		return failed("close", fmt.Errorf("its old terminal %s did not close (%v); nothing was started", old.ID, err))
	}
	recovery := fmt.Sprintf("recover with agentbus up %s --resume", d.Project)
	nt, err := d.Host.Start(ctx, spec)
	if err != nil {
		return failed("start", fmt.Errorf("its old terminal is closed and the start on the current build failed: %v; %s", err, recovery))
	}
	for deadline := d.Now().Add(d.ProofWait); ; d.Sleep(d.ProofPoll) {
		if now, err := d.Bus.Agents(ctx); err == nil {
			if s, ok := now[agent]; ok {
				if in, err := inTerm(ctx, d.Host, s, nt); err == nil && in {
					if err := d.Bus.FinishReload(ctx, bus.SwapEvent{Role: agent, FromProfile: spec.Profile, ToProfile: spec.Profile,
						By: drainBy, Reason: "holder drain", NewPID: s.PID, NewPane: string(nt.ID)}); err != nil {
						return fmt.Errorf("moved to %s (pid %d), but the ledger was not written: %w", nt.ID, s.PID, err)
					}
					_, _ = d.Bus.AppendEvent(ctx, bus.TermEvent{Kind: bus.EventRelaunch, Agent: agent, By: drainBy,
						Detail: fmt.Sprintf("holder drain: from build %s to %s", old.Build, nt.Build)})
					return nil
				}
			}
		}
		if !d.Now().Before(deadline) || ctx.Err() != nil {
			return failed("proof", fmt.Errorf("no proof from its new terminal %s after %s; that terminal is left running (check agentbus agents), %s",
				nt.ID, d.ProofWait, recovery))
		}
	}
}

// Run runs the drain and returns its exit code.
func Run(ctx context.Context, d Deps, out io.Writer) int {
	if err := inventory(ctx, d); err != nil {
		fmt.Fprintf(out, "holder drain: %v\n", err)
		return 1
	}
	olds, err := oldTerms(ctx, d)
	if err != nil {
		fmt.Fprintf(out, "holder drain: the holders: %v\n", err)
		return 1
	}
	if len(olds) > 0 {
		for _, t := range olds { // the first look: the CPU samples the second one compares
			d.Inspect(ctx, t)
		}
		d.Sleep(d.Window)
	}
	skipped := 0
	for _, t := range olds {
		err := move(ctx, d, t)
		var skip errSkip
		switch {
		case errors.As(err, &skip):
			fmt.Fprintf(out, "%s skipped: %s (build %s); run holder drain again later\n", t.Name, skip.why, t.Build)
			skipped++
		case err != nil:
			fmt.Fprintf(out, "%s: %v\nholder drain stopped\n", t.Name, err)
			return 1
		default:
			fmt.Fprintf(out, "%s: moved to the current build, its old terminal closed\n", t.Name)
		}
	}
	if skipped > 0 {
		fmt.Fprintf(out, "holder drain: %d agent(s) left on an old build\n", skipped)
		return Skipped
	}
	// Drained only if every holder still answers and none holds an old
	// terminal now: a holder that went silent meanwhile may hold one.
	if err := inventory(ctx, d); err != nil {
		fmt.Fprintf(out, "holder drain: %v\n", err)
		return 1
	}
	if left, err := oldTerms(ctx, d); err != nil || len(left) > 0 {
		fmt.Fprintf(out, "holder drain: %d terminal(s) still on an old build (%v); run it again\n", len(left), err)
		return 1
	}
	if err := d.GC(); err != nil {
		fmt.Fprintf(out, "holder drain: drained, but the gc failed: %v\n", err)
		return 1
	}
	if len(olds) > 0 {
		_, _ = d.Bus.AppendEvent(ctx, bus.TermEvent{Kind: bus.EventHolderDrained, By: drainBy, Detail: fmt.Sprintf("%d agent(s) moved", len(olds))})
	}
	fmt.Fprintln(out, "holder drain: nothing left on an old build")
	return 0
}
