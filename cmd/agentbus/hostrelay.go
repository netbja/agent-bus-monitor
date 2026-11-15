package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The relay's command side (herdr exit, spec 2026-11-10-herdr-exit-design.md
// §4.2): a host command run inside an agent's terminal (AGENTBUS_TERM_ID
// set: the holder would refuse it) sends the dispatcher a host directive
// and waits for its reply, never for the operation, whose outcome follows
// as a reply of its own. It declares failure only once the deadline has
// passed by Redis TIME and a last read finds no reply.

// relayFailText is the spec's message when the dispatcher does not answer.
const relayFailText = "the dispatcher did not answer within 5 s: from inside an agent's terminal, host commands go through it. Nothing was done. Start it (agentbus service start dispatch) or run the command from your own shell."

// relayPoll is how often the command reads for its reply.
const relayPoll = 100 * time.Millisecond

// hostReply reads the dispatcher's reply (a seam for the tests).
var hostReply = func(ctx context.Context, b *bus.Bus, id, to string) (string, bool, error) {
	return b.HostReply(ctx, id, to)
}

// relayHostOp sends o and waits for the dispatcher's reply until window
// after the directive's id (Redis time). Exit 0 when accepted, 1 otherwise.
func relayHostOp(ctx context.Context, b *bus.Bus, o bus.HostOp, window time.Duration, out, errw io.Writer) int {
	what := o.Op
	if o.Agent != "" {
		what += " " + o.Agent
	}
	// The deadline is Redis TIME at the send, never later than the entry's
	// own id: a forged entry ahead of the clock pushes every later id
	// forward (security S10a-1), and must not stretch the wait.
	sent, err := b.ServerTime(ctx)
	if err != nil {
		fmt.Fprintf(errw, "%s: the broker's clock: %v\n", what, err)
		return 1
	}
	id, err := b.SendHostOp(ctx, o)
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\n", what, err)
		return 1
	}
	ms, err := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
	if err != nil {
		fmt.Fprintf(errw, "%s: unreadable directive id %q\n", what, id)
		return 1
	}
	deadline := min(ms, sent.UnixMilli()) + window.Milliseconds()
	for {
		reply, ok, err := hostReply(ctx, b, id, o.From)
		if err == nil && ok {
			return relayReply(what, reply, out, errw)
		}
		now, terr := b.ServerTime(ctx)
		if terr == nil && now.UnixMilli() > deadline {
			if reply, ok, err := hostReply(ctx, b, id, o.From); err == nil && ok { // once more, past the deadline
				return relayReply(what, reply, out, errw)
			}
			// Giving up is atomic against the accept (reviewer-codex): the
			// directive withdrawn, the dispatcher can never act on it, however
			// late it reads it; withdrawn too late, its reply is there.
			o.ID = id
			withdrawn, err := b.WithdrawHostOp(ctx, o)
			switch {
			case err != nil:
				fmt.Fprintf(errw, "%s: no answer, and the directive could not be withdrawn (%v): it may still run: its outcome comes as a reply (agentbus history -a dispatch)\n", what, err)
				return 1
			case withdrawn:
				fmt.Fprintf(errw, "%s: %s\n", what, relayFailText)
				return 1
			}
			if reply, ok, err := b.HostReply(ctx, id, o.From); err == nil && ok {
				return relayReply(what, reply, out, errw)
			}
			fmt.Fprintf(errw, "%s: the dispatcher handled it, but its reply could not be read\n", what)
			return 1
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(errw, "%s: %v\n", what, ctx.Err())
			return 1
		case <-time.After(relayPoll):
		}
	}
}

// relayReply prints the dispatcher's reply: accepted is success (the
// outcome follows as a reply), anything else is said as it came.
func relayReply(what, reply string, out, errw io.Writer) int {
	if strings.HasPrefix(reply, bus.HostAccepted) {
		fmt.Fprintf(out, "%s: accepted by the dispatcher (from inside an agent's terminal, it runs host commands); its outcome follows as a reply\n", what)
		return 0
	}
	fmt.Fprintf(errw, "%s: %s\n", what, reply)
	return 1
}
