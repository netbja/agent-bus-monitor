package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

func boolPtr(b bool) *bool { return &b }

// cmdEvent builds the subEvent for a delivered cmd entry. rearm is nil for the
// headless --loop (no wake semantics) and &true for a one-shot delivery.
func cmdEvent(e bus.Event, rearm *bool) subEvent {
	ev := subEvent{
		Event: "cmd", Rearm: rearm, ID: e.ID, Type: e.Type, From: e.From, Target: e.Target, Ref: e.Ref, Body: e.Message,
		Task: e.Task, ExpiresAt: e.ExpiresAt, Delivery: e.Delivery, DuplicatePossible: e.Recovered, Attempt: e.Attempt, TextComplete: e.TextComplete,
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

// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. floor is the stream-id floor passed to WatchCmd ("" or
// "0" = no floor). WatchCmdDelivery owns the receiver lease and output/ACK
// ordering. Normal return releases it; process death leaves only its bounded TTL.
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, floor string, loop bool, out io.Writer) int {
	if !bus.ValidName(agent) {
		emit(out, subEvent{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent " + agent})
		return 1
	}
	if loop {
		err := b.WatchCmdDelivery(ctx, agent, consumer, floor, func(e bus.Event) (bool, error) { return false, emit(out, cmdEvent(e, nil)) })
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
	werr := b.WatchCmdDelivery(wctx, agent, consumer, floor, func(e bus.Event) (bool, error) {
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
