package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// subEvent is the single JSON object subscribe emits per fire. One parse gives
// the caller the payload (event=cmd), the cursor to persist (id), and whether
// to re-arm (rearm). rearm is a *bool so fatal's rearm:false survives omitempty
// while --loop entries omit the field entirely.
type subEvent struct {
	V                 int    `json:"v"`
	Event             string `json:"event"`
	Rearm             *bool  `json:"rearm,omitempty"`
	ID                string `json:"id,omitempty"`
	Type              string `json:"type,omitempty"`
	From              string `json:"from,omitempty"`
	Target            string `json:"target,omitempty"`
	Ref               string `json:"ref,omitempty"`
	Body              string `json:"body,omitempty"`
	Msg               string `json:"msg,omitempty"`
	Task              string `json:"task,omitempty"`
	ExpiresAt         int64  `json:"expires_at,omitempty"`
	Delivery          string `json:"delivery,omitempty"`
	DuplicatePossible bool   `json:"duplicate_possible,omitempty"`
	Attempt           int64  `json:"attempt,omitempty"`
	TextComplete      string `json:"text_complete,omitempty"`
	Human             bool   `json:"human,omitempty"` // written by a person (busmon), additive in v1
	Stale             bool   `json:"stale,omitempty"`
	AgeS              int64  `json:"age_s,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// cmdEvent builds the subEvent for a delivered cmd entry. rearm is nil for the
// headless --loop (no wake semantics) and &true for a one-shot delivery.
func cmdEvent(e bus.Event, rearm *bool) subEvent {
	ev := subEvent{
		Event: "cmd", Rearm: rearm, ID: e.ID, Type: e.Type, From: e.From, Target: e.Target, Ref: e.Ref, Body: e.Message,
		Task: e.Task, ExpiresAt: e.ExpiresAt, Delivery: e.Delivery, DuplicatePossible: e.Recovered, Attempt: e.Attempt, TextComplete: e.TextComplete,
		Stale: e.Stale, AgeS: e.AgeS, Human: e.Human,
	}
	if e.Delivery == "missing" || e.Delivery == "expired" {
		ev.Event = "error"
		ev.Body = ""
		ev.Msg = "command " + e.Delivery + "; no actionable payload"
	}
	return ev
}

// emit writes one subEvent as a single JSON line, stamping the protocol version
// so every variant (cmd/heartbeat/error/fatal) carries "v".
func emit(out io.Writer, ev subEvent) error {
	ev.V = bus.ProtocolVersion
	b, _ := json.Marshal(ev)
	n, err := fmt.Fprintln(out, string(b))
	if err == nil && n != len(b)+1 {
		return io.ErrShortWrite
	}
	return err
}

// briefLine is one drained entry for an agent to read (agent-brief): 'id from
// [type ref task=… expires=… attempt=…]: body', the body's whole text on one
// line (\\ and every line breaker escaped), a person's entry, a possible
// duplicate and a stale one marked; an error or fatal event
// is 'id error: msg', its message escaped too. Never JSON: the agent needs no jq.
func briefLine(ev subEvent) string {
	if ev.Event != "cmd" { // error and fatal alike: one escaped error line (reviewer-codex)
		return strings.TrimSpace(ev.ID + " error: " + briefEscaper.Replace(ev.Msg))
	}
	from := ev.From
	if ev.Human {
		from += " (person)"
	}
	kind := ev.Type
	if ev.Ref != "" {
		kind += " " + ev.Ref
	}
	// A tracked request reads as one (foureyes): its task to accept, its
	// deadline (never accept past it), a redelivery's attempt.
	if ev.Task != "" {
		kind += " task=" + ev.Task
	}
	if ev.ExpiresAt > 0 {
		kind += " expires=" + time.UnixMilli(ev.ExpiresAt).Format(time.RFC3339)
	}
	if ev.Attempt > 1 {
		kind += fmt.Sprintf(" attempt=%d", ev.Attempt)
	}
	line := ev.ID + " " + from + " [" + kind + "]: " + briefEscaper.Replace(ev.Body)
	if ev.DuplicatePossible {
		line += " (duplicate?)" // check agentbus request before redoing it
	}
	if ev.Stale {
		line += " (stale)"
	}
	return line
}

var briefEscaper = strings.NewReplacer(`\`, `\\`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`, "\t", `\t`,
	"\v", `\v`, "\f", `\f`, "\u0085", `\u0085`, "\u2028", `\u2028`, "\u2029", `\u2029`) // every line breaker

// emitBrief writes ev as one briefLine.
func emitBrief(out io.Writer, ev subEvent) error {
	_, err := fmt.Fprintln(out, briefLine(ev))
	return err
}

// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. floor is the stream-id floor passed to WatchCmd ("" or
// "0" = no floor). WatchCmdDelivery owns the receiver lease and output/ACK
// ordering. Normal return releases it; process death leaves only its bounded TTL.
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, floor string, fromNow, loop bool, out io.Writer) int {
	watch := b.WatchCmdDelivery
	if fromNow { // no --since: the backlog is skipped, a person's entries are not
		watch = b.WatchCmdDeliveryFromNow
	}
	if !bus.ValidName(agent) {
		emit(out, subEvent{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent " + agent})
		return 1
	}
	if loop {
		err := watch(ctx, agent, consumer, floor, func(e bus.Event) (bool, error) { return false, emit(out, cmdEvent(e, nil)) })
		if err != nil && !errors.Is(err, context.Canceled) {
			_ = emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: err.Error()})
			return 75
		}
		return 0
	}

	var last bus.Event
	wctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	emitted := false
	werr := watch(wctx, agent, consumer, floor, func(e bus.Event) (bool, error) {
		last = e
		err := emit(out, cmdEvent(e, boolPtr(true)))
		emitted = true // even a partial write must not be followed by a second JSON object
		return true, err
	})
	switch {
	case werr == nil:
		if last.Delivery == "missing" || last.Delivery == "expired" {
			return 75
		}
		return 0
	case emitted:
		// Complete output may precede ACK failure. The event already says uncertain;
		// keep stdout one-shot, report only through the process status.
		return 75
	case errors.Is(werr, context.DeadlineExceeded):
		emit(out, subEvent{Event: "heartbeat", Rearm: boolPtr(true)})
		return 64
	default:
		emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: werr.Error()})
		return 75
	}
}

// drainHorizon is the pinned obligation horizon: a missing group is created at
// now-24h, and entries older than it are delivered flagged stale.
const drainHorizon = bus.DefaultHorizon

const subscribeUsage = "usage: subscribe [--loop] [--since <cursor>] <agent> [idle_seconds] | subscribe --drain [--brief] <agent>"

// errDrainUsage: --drain takes no cursor, no loop and no idle window (exit 2).
var errDrainUsage = errors.New("--drain cannot be combined with --since, --loop or idle_seconds; --brief needs --drain")

type subscribeArgs struct {
	agent, idle, since string
	loop, drain, brief bool
}

// parseSubscribeArgs splits the subscribe/watch flags. A missing agent is a
// plain usage error; --drain with an incompatible flag is errDrainUsage.
func parseSubscribeArgs(rest []string) (subscribeArgs, error) {
	var a subscribeArgs
	rest, a.drain = extractBool(rest, "--drain")
	rest, a.brief = extractBool(rest, "--brief")
	rest, a.loop = extractBool(rest, "--loop")
	rest, a.since = extractFlag(rest, "--since")
	if a.drain && (a.loop || a.since != "" || len(rest) > 1) || a.brief && !a.drain {
		return a, errDrainUsage
	}
	if len(rest) < 1 {
		return a, errors.New(subscribeUsage)
	}
	a.agent = rest[0]
	if len(rest) > 1 {
		a.idle = rest[1]
	}
	return a, nil
}

// runDrain empties the agent's queue: one JSON line per entry (no rearm field),
// or with brief one briefLine per entry (agent-brief), then exits — 0 if anything was output, 64 if the queue was empty, 75 on any
// error. An error after output (e.g. a failed ACK) is reported through the exit
// code only; the emitted lines already say delivery is uncertain.
func runDrain(ctx context.Context, b *bus.Bus, agent, consumer string, out io.Writer, brief bool) int {
	write := emit
	if brief {
		write = emitBrief
	}
	if !bus.ValidName(agent) {
		write(out, subEvent{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent " + agent})
		return 1
	}
	emitted := 0
	_, err := b.DrainCmd(ctx, agent, consumer, drainHorizon, func(e bus.Event) error {
		emitted++ // even a partial write counts: never follow it with an error line
		return write(out, cmdEvent(e, nil))
	})
	return drainExit(out, emitted, err, write)
}

func drainExit(out io.Writer, emitted int, err error, write func(io.Writer, subEvent) error) int {
	switch {
	case err != nil && emitted > 0:
		return 75
	case err != nil:
		// Includes bus.ErrReceiverBusy: nothing was read, output or acknowledged.
		write(out, subEvent{Event: "error", Msg: err.Error()})
		return 75
	case emitted > 0:
		return 0
	default:
		return 64
	}
}
