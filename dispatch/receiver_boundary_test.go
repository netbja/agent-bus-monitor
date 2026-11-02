package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The local-llm peer drains, then receives, under one held lease: an entry
// sent at the drain-to-receive boundary is delivered, the receiver key never
// disappears, and every decision the dispatcher would take for the peer
// while it waits is rule 2 (left alone), never a prompt or an escalation.
func TestReceiverBoundaryKeepsRuleTwo(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if _, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "while down"); err != nil {
		t.Fatal(err)
	}
	r, err := b.TakeReceiver(ctx, "local", "peer", bus.DefaultHorizon, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	stop := make(chan struct{})
	var mu sync.Mutex
	var samples, unarmed int
	var bad []Decision
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			armed, err1 := b.Armed(ctx, "local")
			o, err2 := b.Obligation(ctx, "local", bus.DefaultHorizon)
			if err1 == nil && err2 == nil {
				d := Decide(Policy{Retry: time.Minute, EscalateAfter: 10 * time.Minute, MaxTries: 3}, Input{Obligation: o, Armed: armed, OldestAge: time.Hour})
				mu.Lock()
				samples++
				if !armed {
					unarmed++
				}
				if o.Wakes() && d.Rule != 2 {
					bad = append(bad, d)
				}
				mu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	var got []string
	if _, err := r.Drain(bus.DefaultHorizon, func(e bus.Event) error { got = append(got, e.Message); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "at the boundary"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the poller sees the waiting entry before delivery takes it
	if err := r.Deliver("0", func(e bus.Event) (bool, error) { got = append(got, e.Message); return true, nil }); err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done

	if len(got) != 2 || got[0] != "while down" || got[1] != "at the boundary" {
		t.Fatalf("handled %q, want both entries in order", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if samples < 5 || unarmed != 0 || len(bad) != 0 {
		t.Fatalf("%d samples, %d unarmed, decisions other than rule 2: %+v", samples, unarmed, bad)
	}
}
