package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

type fakeProber struct {
	ping    error
	holders []holder.HolderState
	err     error
}

func (f fakeProber) Ping(context.Context) error { return f.ping }
func (f fakeProber) Holders(context.Context) ([]holder.HolderState, error) {
	return f.holders, f.err
}

// The dispatcher's holder probe (agent-screen-blocked spec §1): its own
// build's holder answering is ready; only another build's holder answering is
// "behind", naming those builds, which of them host a live agent, and which
// could not be listed; none answering is the dial error, as before.
func TestProbeHolders(t *testing.T) {
	ctx := context.Background()
	dial := errors.New("dial unix /run/x/h-own.sock: connect: no such file or directory")
	if err := probeHolders(ctx, fakeProber{}, "b-own"); err != nil {
		t.Fatalf("own holder answers: %v", err)
	}
	err := probeHolders(ctx, fakeProber{ping: dial, holders: []holder.HolderState{
		{Build: "b-886", Listed: true}, {Build: "b-888", Listed: true, Live: 2}}}, "b-own")
	var behind *holderBehindError
	if !errors.As(err, &behind) {
		t.Fatalf("another build answers: err = %v, want holderBehindError", err)
	}
	if behind.Own != "b-own" || strings.Join(behind.Others, ",") != "b-886,b-888" || strings.Join(behind.Hosting, ",") != "b-888" || len(behind.Unlisted) != 0 || !errors.Is(err, dial) {
		t.Fatalf("behind = %+v", behind)
	}
	// reviewer-codex r1: an older holder that answers with no live terminal
	// hosts no agent: the probe says so, it does not claim agents are woken.
	if err := probeHolders(ctx, fakeProber{ping: dial, holders: []holder.HolderState{{Build: "b-886", Listed: true}}}, "b-own"); !errors.As(err, &behind) || len(behind.Hosting) != 0 || len(behind.Unlisted) != 0 {
		t.Fatalf("zero terminals: %v %+v", err, behind)
	}
	// reviewer-codex r2: a holder that answered Describe but whose List
	// failed is unlisted, never "hosts no agent", while another is listed.
	err = probeHolders(ctx, fakeProber{ping: dial, holders: []holder.HolderState{
		{Build: "b-886", Listed: true}, {Build: "b-888", Listed: false}}}, "b-own")
	if !errors.As(err, &behind) || strings.Join(behind.Unlisted, ",") != "b-888" || len(behind.Hosting) != 0 {
		t.Fatalf("partial list: %v %+v", err, behind)
	}
	if err := probeHolders(ctx, fakeProber{ping: dial}, "b-own"); err != dial {
		t.Fatalf("none answers: err = %v, want the dial error", err)
	}
	if err := probeHolders(ctx, fakeProber{ping: dial, err: errors.New("no dir")}, "b-own"); err != dial {
		t.Fatalf("holders fail: err = %v, want the dial error", err)
	}
	// Its own build answering after a failed Ping answers after all.
	if err := probeHolders(ctx, fakeProber{ping: dial, holders: []holder.HolderState{{Build: "b-888", Listed: true}, {Build: "b-own", Listed: true}}}, "b-own"); err != nil {
		t.Fatalf("own build answers: %v", err)
	}
}

// The detail says only what the probe knows: agents still woken on the
// builds that host one; an answering holder with none; unknown when the
// terminals could not be listed.
func TestHolderBehindDetail(t *testing.T) {
	for _, c := range []struct {
		name string
		e    holderBehindError
		want []string
		not  []string
	}{
		{"hosting", holderBehindError{Own: "v0.29.0", Others: []string{"v886", "v888"}, Hosting: []string{"v888"}},
			[]string{"no holder of this build (v0.29.0) runs", "agents on build v888 are still woken", "the holder of build v886 answers but hosts no agent", "a pop, a relaunch or a reload fails until agentbus up demo starts it"}, nil},
		{"none hosted", holderBehindError{Own: "v0.29.0", Others: []string{"v886"}},
			[]string{"the holder of build v886 answers but hosts no agent", "agentbus up demo"}, []string{"still woken"}},
		{"not listed", holderBehindError{Own: "v0.29.0", Others: []string{"v888"}, Unlisted: []string{"v888"}},
			[]string{"the holder of build v888 answers; its agents could not be listed", "agentbus up demo"}, []string{"still woken", "hosts no agent"}},
	} {
		d := c.e.detail("demo")
		for _, w := range c.want {
			if !strings.Contains(d, w) {
				t.Errorf("%s: lacks %q: %q", c.name, w, d)
			}
		}
		for _, w := range c.not {
			if strings.Contains(d, w) {
				t.Errorf("%s: says %q: %q", c.name, w, d)
			}
		}
	}
}

// foureyes: the service's HostPing is the probe, not the bare Ping of its
// own build: built from the host it is given, for this project and build.
func TestServiceHostPing(t *testing.T) {
	dial := errors.New("dial: no such file")
	var gotProject string
	ping := serviceHostPing("demo", "b-own", func(project string) (holderProber, error) {
		gotProject = project
		return fakeProber{ping: dial, holders: []holder.HolderState{{Build: "b-888", Listed: true, Live: 1}}}, nil
	})
	var behind *holderBehindError
	if err := ping(context.Background()); !errors.As(err, &behind) || behind.Own != "b-own" || gotProject != "demo" {
		t.Fatalf("err %v, behind %+v, project %q", err, behind, gotProject)
	}
	bad := errors.New("invalid name")
	if err := serviceHostPing("demo", "b-own", func(string) (holderProber, error) { return nil, bad })(context.Background()); err != bad {
		t.Fatalf("constructor error: %v", err)
	}
}

// The service publishes holder-behind with a detail the person can act on,
// and the dial error kept as the error.
func TestServiceRunDispatchHolderBehind(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	var behind atomic.Bool
	d.HostPing = func(context.Context) error {
		if behind.Load() {
			return &holderBehindError{Own: "v0.29.0", Others: []string{"v0.28.0-888"}, Hosting: []string{"v0.28.0-888"}, err: errors.New("dial unix h-own.sock: no such file")}
		}
		return nil
	}
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-ctx.Done()
		onLease(false)
		return 0
	}
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	waitState(t, b, service.Dispatch, "ready")
	behind.Store(true)
	r := waitState(t, b, service.Dispatch, "holder-behind")
	for _, want := range []string{"no holder of this build (v0.29.0)", "v0.28.0-888", "still woken", "a pop, a relaunch or a reload fails", "agentbus up " + b.Project()} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("detail lacks %q: %q", want, r.Detail)
		}
	}
	if !strings.Contains(r.Error, "no such file") {
		t.Errorf("error = %q", r.Error)
	}
	behind.Store(false)
	waitState(t, b, service.Dispatch, "ready")
}

// foureyes (the merge hazard with Q1): the service's deps probe every
// holder, not only its own build's Ping.
func TestServiceRunDepsProbeEveryHolder(t *testing.T) {
	saved := newProbeHost
	t.Cleanup(func() { newProbeHost = saved })
	newProbeHost = func(string) (holderProber, error) {
		return fakeProber{ping: errors.New("dial: no such file"), holders: []holder.HolderState{{Build: "b-888", Listed: true, Live: 1}}}, nil
	}
	d := serviceRunDepsFor("demo", "", "h1", service.Paths{Home: t.TempDir()})
	var behind *holderBehindError
	if err := d.HostPing(context.Background()); !errors.As(err, &behind) || strings.Join(behind.Hosting, ",") != "b-888" {
		t.Fatalf("the deps' HostPing = %v, want the all-holders probe", err)
	}
}
