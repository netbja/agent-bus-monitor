package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Runner is the peer's life on the bus (spec, "How it reads the bus"): one
// receiver lease from before the drain to its exit, a drain, then delivery
// under the same lease, a turn per entry. Its only bus writes are its own
// status, the reply on the entry's thread (for a tracked request, its accept
// and completion, which writes that reply) and its report.
type Runner struct {
	Bus        *bus.Bus
	Agent      string
	Consumer   string
	Ident      bus.AgentIdent // its verified identity, as agentbus status reads it
	Turn       Turn
	RetryEvery time.Duration // an endpoint fault: retried this often (30 s)
	LeaseWait  time.Duration // a lease held elsewhere: given up after this
	Out        io.Writer     // the pane's transcript

	report func(ctx context.Context, agent, kind, msg string) (string, error) // Bus.Report; a test's
	accept func(ctx context.Context, task, agent string) error                // Bus.AcceptRequest; a test's
}

// Run holds the lease until ctx ends. A lease held by another peer is an
// error (two peers under one name). An error while the lease is still held
// (an entry that fails, a bus write refused) is retried under that same
// lease, after a backoff, draining again so the pending entry comes first:
// the dispatcher keeps seeing the peer armed. Only a lost lease is released
// and taken again. Every retry backs off (1 s doubling to 30 s, back to 1 s
// after a minute without error), so an error that does not heal idles the
// peer instead of spinning it.
func (r *Runner) Run(ctx context.Context) error {
	r.Turn.Log = func(s string) { r.say("  " + s) }
	backoff, lastErr := time.Second, time.Time{}
	wait := func(c context.Context, why string) bool {
		if time.Since(lastErr) > time.Minute {
			backoff = time.Second
		}
		lastErr = time.Now()
		r.say(fmt.Sprintf("%s; trying again in %s", why, backoff))
		ok := sleep(c, backoff)
		backoff = min(2*backoff, 30*time.Second)
		return ok
	}
	for {
		rc, err := r.Bus.TakeReceiver(ctx, r.Agent, r.Consumer, bus.DefaultHorizon, r.LeaseWait)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, bus.ErrReceiverBusy):
			return fmt.Errorf("another peer holds %s's receiver lease: one peer per name", r.Agent)
		case err != nil:
			if !wait(ctx, "bus unavailable ("+err.Error()+")") {
				return nil
			}
			continue
		}
		r.status(ctx, "idle", "local-llm peer: listening")
		for {
			_, err = rc.Drain(bus.DefaultHorizon, func(e bus.Event) error { return r.handle(ctx, rc.Context(), e) })
			if err == nil {
				err = rc.Deliver("0", func(e bus.Event) (bool, error) { return false, r.handle(ctx, rc.Context(), e) })
			}
			if ctx.Err() != nil || rc.Context().Err() != nil {
				break // stopped, or the lease lost
			}
			if !wait(rc.Context(), "delivery failed ("+err.Error()+"), the lease kept") {
				break
			}
		}
		rc.Close()
		if ctx.Err() != nil {
			return nil
		}
		if !wait(ctx, "receiver lease lost ("+err.Error()+")") {
			return nil
		}
	}
}

// handle answers one entry. A nil return acknowledges it; an error leaves it
// pending (the lease lost, the caller gone, a bus write failed).
func (r *Runner) handle(ctx, turnCtx context.Context, e bus.Event) error {
	switch {
	case e.Type != bus.CmdDirective && e.Type != bus.CmdChallenge && e.Type != bus.CmdReply:
		r.say(fmt.Sprintf("%s %s from %s: noted, no turn", e.ID, e.Type, e.From))
		return nil
	case e.NoWake: // a notice (a gate closed without a verdict, a person's close): no turn
		r.say(fmt.Sprintf("%s notice from %s: %s", e.ID, e.From, firstWords(e.Message)))
		return nil
	case e.Type == bus.CmdReply && r.isPeer(ctx, e.From): // two peers never answer each other forever
		r.say(fmt.Sprintf("%s reply from the peer %s: noted, no turn", e.ID, e.From))
		return nil
	}
	tracked := e.Type == bus.CmdDirective && e.Task != ""
	if tracked {
		accept := r.accept
		if accept == nil {
			accept = r.Bus.AcceptRequest
		}
		if err := accept(ctx, e.Task, r.Agent); err != nil {
			if !errors.Is(err, bus.ErrRequestRefused) {
				return err // the broker: the entry stays pending
			}
			r.say(fmt.Sprintf("%s request %s: %v; not answered", e.ID, e.Task, err))
			return nil
		}
	}
	start := time.Now()
	r.say(fmt.Sprintf("%s %s from %s: %s", e.ID, e.Type, e.From, e.Message))
	r.status(ctx, "working", firstWords(e.Message))
	threadID := e.Ref
	if threadID == "" {
		threadID = e.ID
	}
	thread := ""
	if e.Ref != "" {
		if evs, err := r.Bus.Thread(ctx, e.Ref); err == nil {
			thread = ThreadText(evs)
		}
	}
	var res Result
	blocked := ""
	for {
		var err error
		res, err = r.Turn.Run(turnCtx, e, thread)
		var ef *EndpointError
		if !errors.As(err, &ef) {
			if err != nil {
				return err // the lease lost or the peer stopped: the entry stays pending
			}
			break
		}
		if reason := ef.Error(); reason != blocked {
			blocked = reason
			r.say(reason + "; retrying every " + r.RetryEvery.String())
			r.status(ctx, "blocked", reason)
		}
		if !sleep(turnCtx, r.RetryEvery) {
			return turnCtx.Err()
		}
	}
	if blocked != "" {
		r.status(ctx, "working", firstWords(e.Message))
	}
	report := r.report
	if report == nil {
		report = r.Bus.Report
	}
	// A reply the bus refuses for good (a text it cannot hold, a sender that
	// is no agent name) is said, reported and acknowledged: retried, the same
	// entry would turn again forever (SEC-LLM-1).
	if why := unsendable(e.From, res.Text); why != "" {
		r.say(fmt.Sprintf("%s: the answer cannot be sent (%s); acknowledged", e.ID, why))
		if _, err := report(ctx, r.Agent, bus.ReportNote, fmt.Sprintf("could not send the answer to %s on thread %s: %s", e.From, threadID, why)); err != nil {
			r.say("report: " + err.Error())
		}
		r.status(ctx, "idle", "answer not sent")
		return nil
	}
	var err error
	if tracked {
		_, err = r.Bus.CompleteRequest(ctx, e.Task, r.Agent, res.Text) // writes the reply on the thread
	} else {
		_, err = r.Bus.Cmd(ctx, r.Agent, e.From, bus.CmdReply, threadID, res.Text)
	}
	if err != nil {
		return err // nothing sent: the entry stays pending
	}
	// The answer is out: anything failing from here is said, never a reason
	// to answer again.
	took := time.Since(start).Round(100 * time.Millisecond)
	r.say(fmt.Sprintf("answer (%s, %d tool calls, %d prompt + %d completion tokens):\n%s",
		took, res.Calls, res.Usage.PromptTokens, res.Usage.CompletionTokens, res.Text))
	if _, err := report(ctx, r.Agent, bus.ReportNote, fmt.Sprintf("answered %s on thread %s in %s: %s", e.From, threadID, took, firstWords(res.Text))); err != nil {
		r.say("report: " + err.Error())
	}
	r.status(ctx, "idle", "answered "+e.From)
	return nil
}

// unsendable is why the bus would refuse a reply for good, "" when it
// takes it: the checks Bus.Cmd and CompleteRequest make before writing.
func unsendable(to, text string) string {
	if err := bus.ValidateText(text, bus.CmdMaxRunes); err != nil {
		return err.Error()
	}
	if !bus.ValidName(to) {
		return fmt.Sprintf("the sender %q is no agent name", to)
	}
	return ""
}

// isPeer reports whether agent is a local-llm peer, as its snapshot says.
func (r *Runner) isPeer(ctx context.Context, agent string) bool {
	snaps, err := r.Bus.Agents(ctx)
	return err == nil && snaps[agent].Client == "local-llm"
}

// status declares the peer's state with its own identity; a failure is said
// in the pane, never fatal (the reply is what matters).
func (r *Runner) status(ctx context.Context, state, msg string) {
	if _, err := r.Bus.Status(ctx, r.Agent, state, msg, r.Ident); err != nil {
		r.say("status " + state + ": " + err.Error())
	}
}

// say writes one line to the pane. Its text comes from the bus, the model
// and the endpoint, so terminal controls are stripped (SEC-LLM-2).
func (r *Runner) say(s string) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, "%s %s\n", time.Now().Format("15:04:05"), paneText(s))
	}
}

// paneText keeps newlines and tabs and turns every other control rune (C0,
// DEL, C1: escape sequences, OSC 52, a bell) into a space, as
// bus.SanitizeReportFull does: the terminal is never handed a control.
func paneText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// firstWords is a status line: the text's first words, on one line.
func firstWords(s string) string {
	f := strings.Fields(s)
	if len(f) > 12 {
		f = append(f[:12], "...")
	}
	return strings.Join(f, " ")
}

// sleep waits d; false when ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
