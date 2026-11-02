package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Runner is the peer's life on the bus (spec, "How it reads the bus"): one
// receiver lease from before the drain to its exit, a drain, then delivery
// under the same lease, a turn per entry. Its only bus writes are its own
// status, the reply on the entry's thread and its report.
type Runner struct {
	Bus        *bus.Bus
	Agent      string
	Consumer   string
	Ident      bus.AgentIdent // its verified identity, as agentbus status reads it
	Turn       Turn
	RetryEvery time.Duration // an endpoint fault: retried this often (30 s)
	LeaseWait  time.Duration // a lease held elsewhere: given up after this
	Out        io.Writer     // the pane's transcript
}

// Run holds the lease until ctx ends. A lease held by another peer is an
// error (two peers under one name); a lost lease is taken again, and the
// queue drained again, before receiving.
func (r *Runner) Run(ctx context.Context) error {
	r.Turn.Log = func(s string) { r.say("  " + s) }
	backoff := time.Second
	for {
		rc, err := r.Bus.TakeReceiver(ctx, r.Agent, r.Consumer, bus.DefaultHorizon, r.LeaseWait)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, bus.ErrReceiverBusy):
			return fmt.Errorf("another peer holds %s's receiver lease: one peer per name", r.Agent)
		case err != nil:
			r.say("bus unavailable (" + err.Error() + "); retrying")
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff = min(2*backoff, 30*time.Second)
			continue
		}
		backoff = time.Second
		r.status(ctx, "idle", "local-llm peer: listening")
		_, err = rc.Drain(bus.DefaultHorizon, func(e bus.Event) error { return r.handle(ctx, rc.Context(), e) })
		if err == nil {
			err = rc.Deliver("0", func(e bus.Event) (bool, error) { return false, r.handle(ctx, rc.Context(), e) })
		}
		rc.Close()
		if ctx.Err() != nil {
			return nil
		}
		r.say("receiver lease lost (" + err.Error() + "); taking it again")
	}
}

// handle answers one entry. A nil return acknowledges it; an error leaves it
// pending (the lease lost, the caller gone, a bus write failed).
func (r *Runner) handle(ctx, turnCtx context.Context, e bus.Event) error {
	switch e.Type {
	case bus.CmdDirective, bus.CmdChallenge, bus.CmdReply:
	default:
		r.say(fmt.Sprintf("%s %s from %s: noted, no turn", e.ID, e.Type, e.From))
		return nil
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
	if _, err := r.Bus.Cmd(ctx, r.Agent, e.From, bus.CmdReply, threadID, res.Text); err != nil {
		return err
	}
	took := time.Since(start).Round(100 * time.Millisecond)
	r.say(fmt.Sprintf("answer (%s, %d tool calls, %d prompt + %d completion tokens):\n%s",
		took, res.Calls, res.Usage.PromptTokens, res.Usage.CompletionTokens, res.Text))
	_, err := r.Bus.Report(ctx, r.Agent, bus.ReportNote, fmt.Sprintf("answered %s on thread %s in %s: %s", e.From, threadID, took, firstWords(res.Text)))
	if err != nil {
		return err
	}
	r.status(ctx, "idle", "answered "+e.From)
	return nil
}

// status declares the peer's state with its own identity; a failure is said
// in the pane, never fatal (the reply is what matters).
func (r *Runner) status(ctx context.Context, state, msg string) {
	if _, err := r.Bus.Status(ctx, r.Agent, state, msg, r.Ident); err != nil {
		r.say("status " + state + ": " + err.Error())
	}
}

func (r *Runner) say(s string) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, "%s %s\n", time.Now().Format("15:04:05"), s)
	}
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
