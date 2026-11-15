// Package holderdrain is `agentbus holder drain` (herdr exit spec 4.4).
package holderdrain

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// `agentbus holder drain <project>` (herdr exit spec 4.4): an older build's
// holder never ends by itself while its agents live (/clear ends no
// terminal), so the person finishes the drain: a rolling relaunch of each
// terminal still on an old build, one agent at a time, through the
// relaunch-close order (start the new terminal, wait for the agent's proof,
// close the old one). An agent working, with a person active or a draft, or
// with background work (4.3b, unknown counts as running) is skipped; the
// command says so and is run again later. Never automatic.

// Deps is what the drain reads and does; production wires the
// project's holders, the bus and the service gc (cmd/agentbus).
type Deps struct {
	Project string
	Host    termhost.Host
	// Old: the terminal is on a build other than the current one.
	Old func(termhost.Term) bool
	// Inspect is the dispatcher's look at a terminal (HostRuntime.Inspect):
	// its state, a person active, its background work.
	Inspect func(ctx context.Context, t termhost.Term) dispatch.Inspection
	// Declared is each agent's declared state.
	Declared func(ctx context.Context) (map[string]string, error)
	// Resume is the agent's {p}:resume record (nil: a fresh session).
	Resume func(ctx context.Context, agent string) (*bus.ResumeRecord, error)
	// Proved: the agent proved its identity from the new terminal.
	Proved func(ctx context.Context, agent string, t termhost.Term) (bool, error)
	Event  func(ctx context.Context, e bus.TermEvent) error
	GC     func() error
	Sleep  func(time.Duration)
	Now    func() time.Time
	// Window is the background judgement's CPU window: the old terminals are
	// looked at once, then again a window later.
	Window               time.Duration
	ProofWait, ProofPoll time.Duration
	// IgnoreBackground is the person's --ignore-background (spec 4.3b, as
	// for relaunch, swap and suspend): a client with no authoritative
	// source (Codex, Grok, Kimi, agy) is never judged free of background
	// work, so without it the person could never drain those. Working, a
	// person active and a draft still skip.
	IgnoreBackground bool
}

// drainBy is the events' author.
const drainBy = "holder-drain"

// Exit codes: 0 drained, 1 failed, 3 agents skipped (run it again later).
const Skipped = 3

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

// drainSkip is why the agent's terminal is not relaunched now, or "".
func drainSkip(ctx context.Context, d Deps, t termhost.Term, declared map[string]string) string {
	if declared[t.Name] == "working" {
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
		return "its background work is unknown (--ignore-background to relaunch it anyway)"
	case in.BackgroundSkip() != "":
		return in.BackgroundSkip() + " (--ignore-background to relaunch it anyway)"
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

// drainRelaunch starts the agent's new terminal on the current build, waits for
// its proof and closes the old one; an unproved new terminal is closed and
// the old one kept.
func drainRelaunch(ctx context.Context, d Deps, old termhost.Term) error {
	spec := termhost.Spec{Project: d.Project, Name: old.Name}
	rec, err := d.Resume(ctx, old.Name)
	if err != nil {
		return fmt.Errorf("its resume record: %w", err)
	}
	if rec != nil {
		spec.Profile, spec.ResumeSession = rec.Profile, rec.Session
	}
	nt, err := d.Host.Start(ctx, spec)
	if err != nil {
		return fmt.Errorf("start on the current build: %w", err)
	}
	deadline := d.Now().Add(d.ProofWait)
	for {
		ok, err := d.Proved(ctx, old.Name, nt)
		if err == nil && ok {
			break
		}
		if !d.Now().Before(deadline) || ctx.Err() != nil {
			_ = d.Host.Close(ctx, nt.ID)
			return fmt.Errorf("its new terminal was not proved within %s; closed it, the old one kept", d.ProofWait)
		}
		d.Sleep(d.ProofPoll)
	}
	if err := d.Host.Close(ctx, old.ID); err != nil {
		return fmt.Errorf("proved on %s, but its old terminal %s did not close: %w", nt.ID, old.ID, err)
	}
	_ = d.Event(ctx, bus.TermEvent{Kind: bus.EventRelaunch, Agent: old.Name, By: drainBy,
		Detail: fmt.Sprintf("holder drain: from build %s to %s", old.Build, nt.Build)})
	return nil
}

// Run runs the drain and returns its exit code.
func Run(ctx context.Context, d Deps, out io.Writer) int {
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
	declared, err := d.Declared(ctx)
	if err != nil {
		fmt.Fprintf(out, "holder drain: the agents' states: %v\n", err)
		return 1
	}
	skipped := 0
	for _, t := range olds {
		if why := drainSkip(ctx, d, t, declared); why != "" {
			fmt.Fprintf(out, "%s skipped: %s (build %s); run holder drain again later\n", t.Name, why, t.Build)
			skipped++
			continue
		}
		fmt.Fprintf(out, "%s: relaunching on the current build\n", t.Name)
		if err := drainRelaunch(ctx, d, t); err != nil {
			fmt.Fprintf(out, "%s: %v\nholder drain stopped\n", t.Name, err)
			return 1
		}
		fmt.Fprintf(out, "%s: relaunched, its old terminal closed\n", t.Name)
	}
	if skipped > 0 {
		fmt.Fprintf(out, "holder drain: %d agent(s) left on an old build\n", skipped)
		return Skipped
	}
	if err := d.GC(); err != nil {
		fmt.Fprintf(out, "holder drain: drained, but the gc failed: %v\n", err)
		return 1
	}
	if len(olds) > 0 {
		_ = d.Event(ctx, bus.TermEvent{Kind: bus.EventHolderDrained, By: drainBy, Detail: fmt.Sprintf("%d agent(s) relaunched", len(olds))})
	}
	fmt.Fprintln(out, "holder drain: nothing left on an old build")
	return 0
}
