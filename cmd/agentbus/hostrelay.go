package main

import (
	"context"
	"fmt"
	"io"
	"slices"
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

// hostOpFromArgs maps a host command line, run inside an agent's terminal,
// to its directive and project (envProject unless the command names one);
// ok false: not a relayed command, run it as it is. A host command with
// words the closed set cannot carry is refused (ok, err): shortening it
// would run something else than was asked.
func hostOpFromArgs(args []string, envProject, self string) (o bus.HostOp, project string, ok bool, err error) {
	if len(args) == 0 || slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		return o, "", false, nil
	}
	o, project = bus.HostOp{From: self}, envProject
	words := args[1:]
	switch args[0] {
	case "up":
		if slices.Contains(words, "--dry-run") { // starts nothing: it reads the holder, which allows that
			return o, "", false, nil
		}
		// up bootstraps a project and starts its team, the person's gesture;
		// the relay's sender is self-attested (AGENT_BUS_AGENT), so no up is
		// relayed (reviewer-codex on 10b). Dead agents come back by self-repair.
		return o, project, true, fmt.Errorf("up: refused inside an agent's terminal: it is the person's command, run from their own shell (a dead agent comes back by the dispatcher's self-repair); up --dry-run shows what it would do")
	case "pop", "reload", "retire", "swap":
		o.Op = map[string]string{"pop": bus.HostPop, "reload": bus.HostReload, "retire": bus.HostRetire, "swap": bus.HostSwap}[args[0]]
		if len(words) > 0 && !strings.HasPrefix(words[0], "-") {
			o.Agent, words = words[0], words[1:]
		}
		if o.Op == bus.HostPop || o.Op == bus.HostSwap {
			words, o.Profile = extractFlag(words, "--profile")
		}
		if o.Op == bus.HostRetire { // --forget: what the dispatcher tells master to run for a done pop
			var episode string
			words, o.Forget = extractBool(words, "--forget")
			words, episode = extractFlag(words, "--episode")
			words, o.Reason = extractFlag(words, "--reason")
			if episode != "" {
				n, err := strconv.ParseInt(episode, 10, 64)
				if err != nil || n <= 0 {
					return o, project, true, relayRefusal(args[0], relayWords[bus.HostRetire]+" (--episode is a positive number)")
				}
				o.Episode = n
			}
		}
		if o.Op == bus.HostSwap {
			var back bool
			words, back = extractBool(words, "--back")
			if back == (o.Profile != "") {
				return o, project, true, relayRefusal(args[0], "--profile <profile> or --back")
			}
		}
	case "suspend":
		o.Op = bus.HostSuspend
		var p string
		if words, p = extractFlag(words, "--project"); p != "" {
			project = p
		}
	case "resume":
		if len(words) > 0 { // a pause's resume message, or --all: the holder is not needed or not reached from here
			return o, "", false, nil
		}
		o.Op = bus.HostResume
	case "holder":
		if len(words) == 0 || words[0] != "drain" {
			return o, "", false, nil
		}
		o.Op, words = bus.HostDrain, words[1:]
		if len(words) > 0 && !strings.HasPrefix(words[0], "-") {
			project, words = words[0], words[1:]
		}
	default:
		return o, "", false, nil
	}
	if len(words) > 0 {
		return o, project, true, relayRefusal(args[0], "only "+relayWords[o.Op])
	}
	if err := o.Validate(); err != nil {
		return o, project, true, relayRefusal(args[0], relayWords[o.Op]+" ("+err.Error()+")")
	}
	return o, project, true, nil
}

// relayWords: what each relayed command takes from inside a terminal.
var relayWords = map[string]string{
	bus.HostUp: "no up (the person's)", bus.HostPop: "pop <role> [--profile <profile>]", bus.HostReload: "reload <role>",
	bus.HostRetire: "retire <role> [--forget --episode <ms>] [--reason <text>]", bus.HostSwap: "swap <role> (--profile <profile> | --back)",
	bus.HostSuspend: "suspend [--project <p>]", bus.HostResume: "resume", bus.HostDrain: "holder drain <project>",
}

func relayRefusal(cmd, takes string) error {
	return fmt.Errorf("%s: from inside an agent's terminal it goes through the dispatcher, which takes %s; run anything else from your own shell", cmd, takes)
}

// relayFromTerminal is main's: inside a terminal the holder started
// (AGENTBUS_TERM_ID), a host command is sent to the dispatcher instead of
// the holder, which would refuse it. ok false: not a host command.
func relayFromTerminal(ctx context.Context, args []string, host, project string, getenv func(string) string, out, errw io.Writer) (int, bool) {
	if getenv("AGENTBUS_TERM_ID") == "" {
		return 0, false
	}
	self := getenv("AGENT_BUS_AGENT")
	if self == "" {
		self = "anonymous"
	}
	o, project, ok, err := hostOpFromArgs(args, project, self)
	switch {
	case !ok:
		return 0, false
	case err != nil:
		fmt.Fprintln(errw, err)
		return 2, true
	case !bus.ValidName(project):
		fmt.Fprintf(errw, "%s: project required: pass --project <p> or set AGENT_BUS_PROJECT\n", args[0])
		return 2, true
	}
	c, err := bus.Connect(host)
	if err != nil {
		fmt.Fprintf(errw, "%s: Redis connection failed: %v\n", args[0], err)
		return 1, true
	}
	defer c.Close()
	b, err := bus.Open(c, project)
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\n", args[0], err)
		return 1, true
	}
	return relayHostOp(ctx, b, o, bus.HostAcceptWindow, out, errw), true
}
