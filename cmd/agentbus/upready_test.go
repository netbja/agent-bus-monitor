package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// up-broker-ready: a broker docker has just started answers LOADING while it reads its data back,
// so up waits for PING before it marks the project's services; the wait is bounded.

// fakePing answers each call from its script in turn, then the last answer again.
type fakePing struct {
	answers []error
	calls   int
}

func (f *fakePing) ping() error {
	i := min(f.calls, len(f.answers)-1)
	f.calls++
	return f.answers[i]
}

func TestUpWaitsForTheBrokerBeforeMarkingServices(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	loading := errors.New("LOADING Redis is loading the dataset in memory")
	p := &fakePing{answers: []error{loading, loading, nil}}
	var slept []time.Duration
	deps.Ping = p.ping
	deps.Sleep = func(d time.Duration) { slept = append(slept, d) }
	deps.Mark = func(project string, inst bus.ServicesInstall) error {
		if p.calls < 3 {
			t.Fatalf("marked after %d ping(s): the broker was still loading", p.calls)
		}
		u.marks = append(u.marks, inst)
		return nil
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if p.calls != 3 || len(u.marks) != 1 || len(slept) != 2 {
		t.Fatalf("pings = %d, marks = %d, sleeps = %v", p.calls, len(u.marks), slept)
	}
	if !u.m.saw("start demo dispatch") {
		t.Fatalf("services not started: %v", u.m.calls)
	}
}

func TestUpGivesUpOnABrokerThatStaysLoading(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	p := &fakePing{answers: []error{errors.New("LOADING Redis is loading the dataset in memory")}}
	var waited time.Duration
	deps.Ping = p.ping
	deps.Sleep = func(d time.Duration) { waited += d }
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(u.marks) != 0 || u.m.saw("start demo dispatch") {
		t.Fatalf("marked %d, calls %v: nothing may be marked or started on a broker that never answered", len(u.marks), u.m.calls)
	}
	if waited < 5*time.Second || waited > 30*time.Second {
		t.Fatalf("waited %v: the wait must be bounded at a few seconds", waited)
	}
	if !strings.Contains(errw.String(), "LOADING") {
		t.Fatalf("the error must say why: %s", errw.String())
	}
}

func TestUpDoesNotWaitOnAnAnswerThatWillNotChange(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	p := &fakePing{answers: []error{errors.New("WRONGPASS invalid username-password pair")}}
	deps.Ping = p.ping
	deps.Sleep = func(time.Duration) { t.Fatal("slept on an error that no wait fixes") }
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if p.calls != 1 || len(u.marks) != 0 || !strings.Contains(errw.String(), "WRONGPASS") {
		t.Fatalf("pings = %d, marks = %d, stderr %q", p.calls, len(u.marks), errw.String())
	}
}
