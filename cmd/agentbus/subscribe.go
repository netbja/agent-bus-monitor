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
	V      int    `json:"v"`
	Event  string `json:"event"`
	Rearm  *bool  `json:"rearm,omitempty"`
	ID     string `json:"id,omitempty"`
	Type   string `json:"type,omitempty"`
	From   string `json:"from,omitempty"`
	Target string `json:"target,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Body   string `json:"body,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// cmdEvent builds the subEvent for a delivered cmd entry. rearm is nil for the
// headless --loop (no wake semantics) and &true for a one-shot delivery.
func cmdEvent(e bus.Event, rearm *bool) subEvent {
	return subEvent{
		Event: "cmd", Rearm: rearm, ID: e.ID,
		Type: e.Type, From: e.From, Target: e.Target, Ref: e.Ref, Body: e.Message,
	}
}

// emit writes one subEvent as a single JSON line, stamping the protocol version
// so every variant (cmd/heartbeat/error/fatal) carries "v".
func emit(out io.Writer, ev subEvent) {
	ev.V = bus.ProtocolVersion
	b, _ := json.Marshal(ev)
	fmt.Fprintln(out, string(b))
}

// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. floor is the stream-id floor passed to WatchCmd ("" or
// "0" = no floor). It arms a presence lease around the WatchCmd block and always
// disarms on return (the caller os.Exits on the returned code, so this function
// must never os.Exit itself — that would skip the defer).
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, floor string, loop bool, out io.Writer) int {
	if !bus.ValidName(agent) {
		emit(out, subEvent{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent " + agent})
		return 1
	}
	_ = b.Arm(ctx, agent, consumer, idle)       // best-effort observability
	defer b.Disarm(context.Background(), agent) // runs on return (never on os.Exit)

	if loop {
		// Headless continuous mode: keep the lease warm and emit every addressed
		// cmd object; never exit on delivery. rearm is omitted (no wake path).
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			tick := idle / 2
			if tick <= 0 {
				tick = time.Second
			}
			tk := time.NewTicker(tick)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					_ = b.Arm(ctx, agent, consumer, idle)
				}
			}
		}()
		err := b.WatchCmd(ctx, agent, consumer, floor, func(e bus.Event) bool {
			emit(out, cmdEvent(e, nil))
			return false // never "done" → consume continuously
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: err.Error()})
			return 75
		}
		return 0
	}

	var last bus.Event
	wctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	werr := b.WatchCmd(wctx, agent, consumer, floor, func(e bus.Event) bool {
		last = e
		return true // one-shot: stop on the first addressed entry
	})
	switch {
	case werr == nil:
		emit(out, cmdEvent(last, boolPtr(true)))
		return 0
	case errors.Is(werr, context.DeadlineExceeded):
		emit(out, subEvent{Event: "heartbeat", Rearm: boolPtr(true)})
		return 64
	default:
		emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: werr.Error()})
		return 75
	}
}
