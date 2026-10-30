package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// up-broker-ready: a broker docker has just started answers LOADING while it reads its data back,
// so up waits for PING before it marks the project's services; the wait is bounded on the clock.

// fakeBroker answers each ping from its script in turn, then the last answer again, on a fake
// clock: a ping takes cost (a hung broker: its whole timeout), a sleep takes its duration.
type fakeBroker struct {
	answers  []error
	hang     bool // each ping uses its whole timeout
	calls    int
	clock    time.Time
	timeouts []time.Duration
	slept    []time.Duration
}

func (f *fakeBroker) ping(timeout time.Duration) error {
	f.timeouts = append(f.timeouts, timeout)
	if f.hang {
		f.clock = f.clock.Add(timeout)
	}
	i := min(f.calls, len(f.answers)-1)
	f.calls++
	return f.answers[i]
}

func (f *fakeBroker) wire(deps *upDeps) {
	f.clock = time.Unix(1_800_000_000, 0)
	deps.Ping = f.ping
	deps.Now = func() time.Time { return f.clock }
	deps.Sleep = func(d time.Duration) { f.slept = append(f.slept, d); f.clock = f.clock.Add(d) }
}

var errLoading = errors.New("LOADING Redis is loading the dataset in memory")

func TestUpWaitsForTheBrokerBeforeMarkingServices(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	f := &fakeBroker{answers: []error{errLoading, errLoading, nil}}
	f.wire(&deps)
	deps.Mark = func(project string, inst bus.ServicesInstall) error {
		if f.calls < 3 {
			t.Fatalf("marked after %d ping(s): the broker was still loading", f.calls)
		}
		u.marks = append(u.marks, inst)
		return nil
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if f.calls != 3 || len(u.marks) != 1 || len(f.slept) != 2 {
		t.Fatalf("pings = %d, marks = %d, sleeps = %v", f.calls, len(u.marks), f.slept)
	}
	if !u.m.saw("start demo dispatch") {
		t.Fatalf("services not started: %v", u.m.calls)
	}
}

func TestUpGivesUpOnABrokerThatStaysLoading(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	f := &fakeBroker{answers: []error{errLoading}}
	f.wire(&deps)
	start := f.clock
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(u.marks) != 0 || u.m.saw("start demo dispatch") {
		t.Fatalf("marked %d, calls %v: nothing may be marked or started on a broker that never answered", len(u.marks), u.m.calls)
	}
	if waited := f.clock.Sub(start); waited < 5*time.Second || waited > brokerWait {
		t.Fatalf("waited %v: the wait must be bounded at a few seconds", waited)
	}
	if !strings.Contains(errw.String(), "LOADING") {
		t.Fatalf("the error must say why: %s", errw.String())
	}
}

// reviewer-codex: the pings' own time counts. A broker that hangs makes each ping use its whole
// timeout; the wait still ends by the deadline, and no ping is given more than is left.
func TestUpBrokerWaitCountsThePingsOwnTime(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	hung := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	f := &fakeBroker{answers: []error{hung}, hang: true}
	f.wire(&deps)
	start := f.clock
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if waited := f.clock.Sub(start); waited > brokerWait {
		t.Fatalf("waited %v, past the %v budget (pings %v, sleeps %v)", waited, brokerWait, f.timeouts, f.slept)
	}
	if f.calls < 2 || len(u.marks) != 0 {
		t.Fatalf("pings = %d (%v), marks = %d", f.calls, f.timeouts, len(u.marks))
	}
	for _, to := range f.timeouts {
		if to <= 0 || to > brokerAttempt {
			t.Fatalf("a ping was given %v (cap %v): %v", to, brokerAttempt, f.timeouts)
		}
	}
}

func TestUpDoesNotWaitOnAnAnswerThatWillNotChange(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	f := &fakeBroker{answers: []error{errors.New("WRONGPASS invalid username-password pair")}}
	f.wire(&deps)
	deps.Sleep = func(time.Duration) { t.Fatal("slept on an error that no wait fixes") }
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if f.calls != 1 || len(u.marks) != 0 || !strings.Contains(errw.String(), "WRONGPASS") {
		t.Fatalf("pings = %d, marks = %d, stderr %q", f.calls, len(u.marks), errw.String())
	}
}

// What a wait can fix, and what it cannot: each retried form on its own, so no branch of
// brokerStarting rides on another.
func TestBrokerStarting(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{errLoading, true},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")}, true},
		{context.DeadlineExceeded, true},
		{io.EOF, true},
		{syscall.ECONNREFUSED, true},
		{syscall.ECONNRESET, true},
		{errors.New("WRONGPASS invalid username-password pair"), false},
		{errors.New("NOAUTH Authentication required."), false},
		{errors.New("ERR unknown command"), false},
	} {
		if got := brokerStarting(c.err); got != c.want {
			t.Errorf("brokerStarting(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
