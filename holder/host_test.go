package holder

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/contract"
)

// hostHarness is the contract's harness on a real test holder: role coder
// runs the fake client in fake-ctl mode, driven by files; a person is a
// focused viewer attached to the terminal.
type hostHarness struct {
	t       *testing.T
	h       *Host
	sock    string
	ctl     string
	mu      sync.Mutex
	viewers map[termhost.TermID]*Client
}

func newHostHarness(t *testing.T) contract.Harness {
	ctl := t.TempDir()
	roles := testRoles(t)
	var sock string
	_, sock = newTestHolderWith(t, func(c *Config) {
		c.SubmitWait, c.PersonQuiet = time.Second, 100*time.Millisecond
		c.Launch = func(s Start) (Command, error) {
			if s.Role == "coder" {
				cmd, err := roles(Start{Role: "fake"})
				cmd.Env = []string{"HOLDER_TEST_MODE=fake", "FAKE=fake-ctl", "FAKE_CTL=" + ctl}
				return cmd, err
			}
			return roles(s)
		}
	})
	rundir := filepath.Dir(filepath.Dir(filepath.Dir(sock)))
	h, err := NewHost("demo", "test", func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return rundir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return &hostHarness{t: t, h: h, sock: sock, ctl: ctl, viewers: map[termhost.TermID]*Client{}}
}

func (x *hostHarness) Host() termhost.Host { return x.h }

// file sets or clears a control file of id's program, and waits for the
// program to have read it (its 20 ms tick) and the screen to settle.
func (x *hostHarness) file(id termhost.TermID, name string, on bool) {
	_, term, _ := strings.Cut(string(id), "/")
	dir := filepath.Join(x.ctl, term)
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, name)
	if on {
		os.WriteFile(p, nil, 0o600)
	} else {
		os.Remove(p)
	}
	time.Sleep(150 * time.Millisecond)
}

func (x *hostHarness) SetReady(id termhost.TermID, ready bool) { x.file(id, "notready", !ready) }

func (x *hostHarness) SetInputEmpty(id termhost.TermID, empty bool) {
	x.file(id, "notempty", !empty)
	if empty {
		x.file(id, "reset", true)
	}
}

func (x *hostHarness) SetLands(id termhost.TermID, lands bool) { x.file(id, "noland", !lands) }

// SetPersonActive attaches a focused viewer (a person at the terminal), or
// detaches it.
func (x *hostHarness) SetPersonActive(id termhost.TermID, active bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	_, term, _ := strings.Cut(string(id), "/")
	if v, ok := x.viewers[id]; ok && !active {
		control(x.t, v, Detach{})
		v.Close()
		delete(x.viewers, id)
		time.Sleep(50 * time.Millisecond)
	} else if !ok && active {
		x.viewers[id] = attached(x.t, x.sock, Attach{Term: term, Cols: 80, Rows: 24})
		time.Sleep(50 * time.Millisecond)
	}
}

func (x *hostHarness) Submitted(id termhost.TermID) []string {
	sc, err := x.h.Screen(context.Background(), id, 0)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for i := 0; i < len(sc.Lines); i++ {
		s, ok := strings.CutPrefix(sc.Lines[i], "SUBMITTED: ")
		if !ok {
			continue
		}
		for full := len([]rune(sc.Lines[i])) >= 80; full && i+1 < len(sc.Lines); full = len([]rune(sc.Lines[i])) >= 80 {
			i++ // a line wider than the terminal wraps onto the next row
			s += sc.Lines[i]
		}
		out = append(out, strings.TrimRight(s, " "))
	}
	return out
}

// The holder's adapter passes the termhost contract (spec 7 step 6).
func TestHostContract(t *testing.T) {
	contract.Run(t, newHostHarness)
}

// serveIn serves a holder of project demo, build, in rundir.
func serveIn(t *testing.T, rundir, build string, launch func(Start) (Command, error)) {
	t.Helper()
	sock, err := SocketPath(func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return rundir
		}
		return ""
	}, "demo", service.BuildKey(build))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{Project: "demo", Build: build, Socket: sock, Launch: launch, Grace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Shutdown() })
}

func shortRundir(t *testing.T) (string, func(string) string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hh")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o700)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir, func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
}

// An upgrade drains (spec 4.4): the old build's terminals stay listed and
// reachable through their own socket, new ones start on the current build,
// and Describe names the draining build.
func TestHostSpansADrainingBuild(t *testing.T) {
	ctx := context.Background()
	rundir, getenv := shortRundir(t)
	serveIn(t, rundir, "old", testRoles(t))
	serveIn(t, rundir, "new", testRoles(t))
	old, _ := NewHost("demo", "old", getenv)
	a, err := old.Start(ctx, termhost.Spec{Project: "demo", Name: "talker", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := NewHost("demo", "new", getenv)
	b, err := h.Start(ctx, termhost.Spec{Project: "demo", Name: "talker", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(a.ID), service.BuildKey("old")+"/") || !strings.HasPrefix(string(b.ID), service.BuildKey("new")+"/") || a.Build != "old" || b.Build != "new" {
		t.Fatalf("ids %s %s, builds %s %s", a.ID, b.ID, a.Build, b.Build)
	}
	terms, err := h.List(ctx, "demo")
	if err != nil || len(terms) != 2 || terms[0].ID != b.ID || terms[1].ID != a.ID {
		t.Fatalf("list %+v %v", terms, err)
	}
	if ok, err := h.WaitScreen(ctx, a.ID, "ready", 2*time.Second); !ok || err != nil {
		t.Fatalf("the old build's terminal: %v %v", ok, err)
	}
	info, err := h.Describe(ctx)
	if err != nil || len(info.Builds) != 2 || info.Terms != 2 || len(info.Draining) != 1 || info.Draining[0] != "old" {
		t.Fatalf("describe %+v %v", info, err)
	}
	if err := h.Close(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Screen(ctx, termhost.TermID(service.BuildKey("gone")+"/t1"), 0); err == nil {
		t.Fatal("a build with no socket answered")
	}
	for _, bad := range []termhost.TermID{"t1", "../x/t1", termhost.TermID(service.BuildKey("new") + "/")} {
		if _, err := h.Screen(ctx, bad, 0); !errors.Is(err, termhost.ErrUnknownTerm) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// The submit frame carries no text: a wake line built for another agent or
// project than the terminal's is refused before anything is sent, since the
// holder would type its own (spec 4.2).
func TestHostSubmitRefusesAnotherAgentsLine(t *testing.T) {
	ctx := context.Background()
	x := newHostHarness(t).(*hostHarness)
	a, err := x.h.Start(ctx, termhost.Spec{Project: "demo", Name: "coder", Cols: 200, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	for _, who := range [][2]string{{"demo", "master"}, {"other", "coder"}} {
		l, _ := termhost.WakeLine(who[0], who[1], 2, "abc123")
		if err := x.h.Submit(ctx, a.ID, l); !errors.Is(err, termhost.ErrInvalidLine) {
			t.Errorf("%v: %v", who, err)
		}
	}
	if got := x.Submitted(a.ID); len(got) != 0 {
		t.Fatalf("typed %q", got)
	}
	if _, err := x.h.Start(ctx, termhost.Spec{Project: "other", Name: "coder"}); !errors.Is(err, termhost.ErrInvalidName) {
		t.Fatalf("another project's start: %v", err)
	}
}

// Close reports once the program ended: one that ignores SIGHUP is still
// alive until the grace's SIGKILL, and Close returns only after it.
func TestHostCloseWaitsForTheEnd(t *testing.T) {
	ctx := context.Background()
	rundir, getenv := shortRundir(t)
	serveIn(t, rundir, "new", testRoles(t))
	h, _ := NewHost("demo", "new", getenv)
	a, err := h.Start(ctx, termhost.Spec{Project: "demo", Name: "hupper", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	h.WaitScreen(ctx, a.ID, "ready", 2*time.Second)
	start := time.Now()
	if err := h.Close(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	terms, _ := h.List(ctx, "demo")
	if d := time.Since(start); d < 900*time.Millisecond || len(terms) != 1 || terms[0].Alive {
		t.Fatalf("Close returned after %v; %+v", d, terms)
	}
}

// The context bounds the dial and the welcome too (reviewer-codex): a
// socket that accepts but never welcomes ends a call at ctx's deadline.
func TestHostCallHonoursTheContextInTheHandshake(t *testing.T) {
	_, getenv := shortRundir(t)
	sock, err := SocketPath(getenv, "demo", service.BuildKey("new"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // accepted, never welcomed
		}
	}()
	h, _ := NewHost("demo", "new", getenv)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = h.Ping(ctx)
	if d := time.Since(start); err == nil || d > time.Second {
		t.Fatalf("Ping returned %v after %v", err, d)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("not the context's error: %v", err)
	}
}
