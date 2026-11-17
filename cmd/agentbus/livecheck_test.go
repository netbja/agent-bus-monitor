package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
)

// liveFake is a project the check runs on, its recovery scripted: after a
// crash the holder answers again at once, health warns, self-repair
// relaunches master on the next read, coder two reads later, each on its
// session; a wake drains on the next read.
type liveFake struct {
	mu        sync.Mutex
	up, down  int
	upErr     error
	holder    dispatch.ProcID
	crashed   []dispatch.ProcID
	agents    map[string]liveAgent
	pending   map[string]int // a role's reads left before it is relaunched
	swaps     map[string][]bus.SwapEvent
	seq       int
	wakes     map[string]string
	status    health.Status
	detail    string
	resumedOn map[string]string // the session a role is relaunched on
	order     []string          // the relaunches, in order
	noHolder  bool              // the holder never comes back
	next      dispatch.ProcID   // the restarted holder, once stale reads are spent
	stale     int               // reads that still find the killed process (the kill not landed yet)
	failRole  string            // self-repair fails this role
}

func newLiveFake() *liveFake {
	return &liveFake{holder: dispatch.ProcID{PID: 100, Start: 1},
		agents:  map[string]liveAgent{"master": {PID: 11, Session: "s-master"}, "coder": {PID: 12, Session: "s-coder"}},
		pending: map[string]int{}, swaps: map[string][]bus.SwapEvent{}, wakes: map[string]string{},
		status: health.OK, detail: "answers, 2 terminal(s)", resumedOn: map[string]string{}}
}

func (f *liveFake) id() string { f.seq++; return fmt.Sprintf("%d-0", 1000+f.seq) }

func (f *liveFake) Up(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.up++
	return f.upErr
}
func (f *liveFake) Down(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down++
	return nil
}
func (f *liveFake) Roles() []string { return []string{"master", "coder"} }

func (f *liveFake) Agents(context.Context) (map[string]liveAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advance()
	out := map[string]liveAgent{}
	for k, v := range f.agents {
		out[k] = v
	}
	return out, nil
}

// advance is self-repair's pass, at each read (f.mu held).
func (f *liveFake) advance() {
	for _, r := range f.Roles() { // each read brings the queue on
		n, ok := f.pending[r]
		if !ok {
			continue
		}
		if n > 0 {
			f.pending[r] = n - 1
			break // one relaunch per pass, master first
		}
		delete(f.pending, r)
		if r == f.failRole {
			f.swaps[r] = append(f.swaps[r], bus.SwapEvent{ID: f.id(), Role: r, Event: bus.SwapFailed, Reason: "the new pane never proved its process"})
			break
		}
		s := f.agents[r].Session
		if o, ok := f.resumedOn[r]; ok {
			s = o
		}
		f.seq++
		f.agents[r] = liveAgent{PID: 1000 + f.seq, Session: s, At: time.Now()}
		f.swaps[r] = append(f.swaps[r], bus.SwapEvent{ID: f.id(), Role: r, Event: bus.SwapRelaunched})
		f.order = append(f.order, r)
		if len(f.pending) == 0 {
			f.status, f.detail = health.OK, "answers, 2 terminal(s)"
		}
		break
	}
}

func (f *liveFake) Holder(context.Context) (dispatch.ProcID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stale > 0 {
		f.stale--
		if f.stale == 0 {
			f.holder = f.next
		}
		return f.crashed[len(f.crashed)-1], nil
	}
	if f.holder.PID == 0 {
		return dispatch.ProcID{}, errors.New("no holder answers")
	}
	return f.holder, nil
}

func (f *liveFake) Crash(_ context.Context, p dispatch.ProcID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p != f.holder {
		return fmt.Errorf("not the holder: %+v", p)
	}
	f.crashed = append(f.crashed, p)
	f.holder, f.next, f.stale = dispatch.ProcID{}, dispatch.ProcID{}, 2
	if !f.noHolder {
		f.next = dispatch.ProcID{PID: p.PID + 1, Start: p.Start + 1}
	}
	for r, a := range f.agents {
		f.agents[r] = liveAgent{PID: 0, Session: a.Session, At: a.At}
	}
	f.pending["master"], f.pending["coder"] = 0, 2
	f.status, f.detail = health.Warn, "holder build x restarted 3s ago: 2 agent(s) lost: coder, master"
	return nil
}

func (f *liveFake) SwapMark(_ context.Context, role string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.swaps[role]; len(s) > 0 {
		return s[len(s)-1].ID, nil
	}
	return "", nil
}

func (f *liveFake) SwapsSince(_ context.Context, role, mark string) ([]bus.SwapEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advance()
	var out []bus.SwapEvent
	for _, e := range f.swaps[role] {
		if mark == "" || bus.IDLess(mark, e.ID) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *liveFake) Wake(_ context.Context, role string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.wakes[role] = id
	return id, nil
}

func (f *liveFake) Drained(_ context.Context, role, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents[role].PID != 0 && f.wakes[role] == id, nil
}

func (f *liveFake) HolderHealth(context.Context) (health.Status, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.detail, nil
}

func (f *liveFake) Resumed(context.Context, string, string) (bool, error) { return true, nil }

func liveTestOpts() liveOpts {
	d := 2 * time.Second
	return liveOpts{Rounds: 1, Poll: time.Millisecond,
		Deadlines: liveDeadlines{Ready: d, Holder: d, Warn: d, Relaunch: d, Pace: d, Drain: d, Healthy: d}}
}

func TestLiveCheckPasses(t *testing.T) {
	f := newLiveFake()
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), f, liveTestOpts(), &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if f.up != 1 || f.down != 1 || len(f.crashed) != 1 || f.crashed[0] != (dispatch.ProcID{PID: 100, Start: 1}) {
		t.Fatalf("up %d down %d crashed %+v", f.up, f.down, f.crashed)
	}
	if strings.Join(f.order, ",") != "master,coder" {
		t.Fatalf("relaunched %v", f.order)
	}
	for _, want := range []string{"team ready", "holder health ok", "wake master", "wake coder", "crash: holder 100 killed",
		"holder answers again (pid 101)", "health holder warns: restarted", "relaunch master (resumed s-master)",
		"relaunch coder (resumed s-coder)", "health holder ok again", "holder live check passed: 1 round(s)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestLiveCheckRounds(t *testing.T) {
	f := newLiveFake()
	o := liveTestOpts()
	o.Rounds = 3
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), f, o, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if len(f.crashed) != 3 || f.crashed[2].PID != 102 || !strings.Contains(out.String(), "passed: 3 round(s)") {
		t.Fatalf("crashed %+v:\n%s", f.crashed, out.String())
	}
}

// Each failure names its step and what was seen; the project is removed.
func TestLiveCheckFails(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(*liveFake)
		want string
	}{
		"up":            {func(f *liveFake) { f.upErr = errors.New("bootstrap failed") }, "FAIL up: bootstrap failed"},
		"holder gone":   {func(f *liveFake) { f.noHolder = true }, "FAIL holder answers again: not within 2s (no holder answers)"},
		"not resumed":   {func(f *liveFake) { f.resumedOn["coder"] = "s-other" }, "FAIL relaunch coder: on session s-other, not its recorded s-coder"},
		"repair failed": {func(f *liveFake) { f.failRole = "coder" }, "FAIL relaunch coder: self-repair failed: the new pane never proved its process"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newLiveFake()
			c.set(f)
			var out bytes.Buffer
			if code := runLiveCheck(context.Background(), f, liveTestOpts(), &out); code != 1 || !strings.Contains(out.String(), c.want) {
				t.Fatalf("exit %d, want %q:\n%s", code, c.want, out.String())
			}
			if f.down != 1 {
				t.Fatalf("down %d", f.down)
			}
		})
	}
}

// The relaunches out of order (coder before master) fail: self-repair's
// order is master first.
func TestLiveCheckOrder(t *testing.T) {
	ff := &liveSwapped{liveFake: newLiveFake()}
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), ff, liveTestOpts(), &out); code != 1 || !strings.Contains(out.String(), "FAIL relaunch order: coder before master") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// liveSwapped relaunches coder first.
type liveSwapped struct{ *liveFake }

func (s *liveSwapped) Crash(ctx context.Context, p dispatch.ProcID) error {
	if err := s.liveFake.Crash(ctx, p); err != nil {
		return err
	}
	s.mu.Lock()
	s.pending = map[string]int{"coder": 0}
	s.mu.Unlock()
	go func() { // master after coder
		for {
			s.mu.Lock()
			done := len(s.order) > 0
			if done {
				s.pending["master"] = 0
			}
			s.mu.Unlock()
			if done {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return nil
}

// A relaunch whose record predates the crash did not resume: the record is
// the old process's.
func TestLiveCheckWaitsForTheNewRecord(t *testing.T) {
	f := &liveStaleRecord{liveFake: newLiveFake()}
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), f, liveTestOpts(), &out); code != 1 || !strings.Contains(out.String(), "FAIL relaunch master: not within 2s (its new process has not recorded its session yet)") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// liveStaleRecord relaunches with the old record's time.
type liveStaleRecord struct{ *liveFake }

func (s *liveStaleRecord) Agents(ctx context.Context) (map[string]liveAgent, error) {
	as, err := s.liveFake.Agents(ctx)
	for r, a := range as {
		a.At = time.Time{}
		as[r] = a
	}
	return as, err
}

// Without its holder's process the check refuses to crash anything.
func TestLiveCheckNeverKillsAnotherProcess(t *testing.T) {
	f := newLiveFake()
	f.holder = dispatch.ProcID{}
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), f, liveTestOpts(), &out); code != 1 || len(f.crashed) != 0 {
		t.Fatalf("exit %d crashed %+v:\n%s", code, f.crashed, out.String())
	}
}

// --keep leaves the project, and says how to remove it.
func TestLiveCheckKeep(t *testing.T) {
	f := newLiveFake()
	o := liveTestOpts()
	o.Keep, o.Project = true, "lc0a1b2c"
	var out bytes.Buffer
	if code := runLiveCheck(context.Background(), f, o, &out); code != 0 || f.down != 0 || !strings.Contains(out.String(), "kept: lc0a1b2c") {
		t.Fatalf("exit %d down %d:\n%s", code, f.down, out.String())
	}
}
