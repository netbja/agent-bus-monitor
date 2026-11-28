package holder

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/cgroot"
	"github.com/netbja/agent-bus-monitor/service"
)

// resource-limits §12.5, step 4b-2 (spec
// docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md): the
// holder starts each pane through its scope, starts it without one when
// systemd-run ends before its exec, and stops scopes at a terminal's end,
// at its own stop, and in its start sweep.

// fakeScopes records what the holder asks of the user manager.
type fakeScopes struct {
	mu      sync.Mutex
	stops   []string // every unit stopped, in order
	listed  []string
	listErr error
	active  map[string]bool // a build's holder runs
	unsure  map[string]bool // the manager cannot say
	log     bytes.Buffer
	rec     string // where the fake systemd-run writes the unit it was given
	launch  string // "enter" (default), "fail", "silent"
}

func (f *fakeScopes) scopes(t *testing.T) *Scopes {
	f.rec = filepath.Join(t.TempDir(), "units")
	return &Scopes{
		Argv: func(unit string, argv []string) []string {
			script := `echo "$0" >> ` + f.rec + `; printf y >&3; exec 3>&-; exec "$@"`
			switch f.launch {
			case "fail": // systemd-run without a user manager: it ends before its exec
				script = `echo "$0" >> ` + f.rec + `; echo 'Failed to connect to bus'; exit 1`
			case "silent": // the word never comes, the fd stays open
				script = `echo "$0" >> ` + f.rec + `; exec "$@"`
			}
			return append([]string{"/bin/sh", "-c", script, unit}, argv...)
		},
		List: func() ([]string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return slices.Clone(f.listed), f.listErr
		},
		Stop: func(units ...string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.stops = append(f.stops, units...)
			return nil
		},
		Active: func(key string) (bool, error) {
			if f.unsure[key] {
				return false, errors.New("the manager could not say")
			}
			return f.active[key], nil
		},
		Log:       &lockedWriter{mu: &f.mu, w: &f.log},
		EnterWait: 300 * time.Millisecond,
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (f *fakeScopes) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stops)
}

func (f *fakeScopes) logText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.log.String()
}

func (f *fakeScopes) waitStopped(t *testing.T, unit string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if slices.Contains(f.stopped(), unit) {
			return
		}
	}
	t.Fatalf("%s never stopped: %q", unit, f.stopped())
}

var testKey = service.BuildKey("test")

func unitOf(role, term string) string { return cgroot.ScopeUnit("demo", testKey, role, term) }

func scopeOf(s *Server, id string) string {
	t, ok := s.term(id)
	if !ok {
		return "?"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scope
}

// The pane's command runs through the scope launch, named for its terminal,
// and the pid the holder reports is the client's (the pid contract, §12.5).
func TestScopeLaunch(t *testing.T) {
	f := &fakeScopes{}
	s, sock := newTestHolderWith(t, func(c *Config) { c.Scopes = f.scopes(t) })
	cl := dial(t, sock)
	e := startRole(t, cl, "echoer")
	waitScreen(t, cl, e.ID, "hello "+e.ID)
	if b, _ := os.ReadFile(f.rec); strings.TrimSpace(string(b)) != unitOf("echoer", e.ID) {
		t.Errorf("the scope launch was given %q, want %s", b, unitOf("echoer", e.ID))
	}
	if got := scopeOf(s, e.ID); got != unitOf("echoer", e.ID) {
		t.Errorf("the terminal's scope %q", got)
	}
	comm := ""
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && comm != "sleep"; time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile("/proc/" + strconv.Itoa(e.PID) + "/comm")
		comm = strings.TrimSpace(string(b))
	}
	if comm != "sleep" {
		t.Errorf("/proc/%d/comm = %q: the reported pid is not the client's", e.PID, comm)
	}
}

// systemd-run that ends before its exec (no user manager): the pane starts
// as without scopes, said once, never refused.
func TestScopeLaunchFailsBeforeExec(t *testing.T) {
	f := &fakeScopes{launch: "fail"}
	s, sock := newTestHolderWith(t, func(c *Config) { c.Scopes = f.scopes(t) })
	cl := dial(t, sock)
	e := startRole(t, cl, "echoer")
	waitScreen(t, cl, e.ID, "hello "+e.ID)
	if b, _ := os.ReadFile(f.rec); strings.TrimSpace(string(b)) != unitOf("echoer", e.ID) {
		t.Errorf("no scope launch tried: %q", b)
	}
	if got := scopeOf(s, e.ID); got != "" {
		t.Errorf("the terminal's scope %q, want none", got)
	}
	if x := termOf(t, cl, e.ID); !x.Alive {
		t.Errorf("the pane is not alive: %+v", x)
	}
	r, _ := cl.Call(List{})
	if len(r.Terms) != 1 || r.Terms[0].ID != e.ID {
		t.Errorf("terminals %+v", r.Terms)
	}
	if !strings.Contains(f.logText(), "terminal "+e.ID+": no scope") || !strings.Contains(f.logText(), "without its cap") {
		t.Errorf("log %q", f.logText())
	}
	if e2 := startRole(t, cl, "echoer"); e2.ID != "t2" {
		t.Errorf("the next terminal is %s: the failed launch took a number", e2.ID)
	}
}

// No word from agent-enter in time: the launch went on (it may still be
// starting), so the pane keeps its scope, and the holder says it.
func TestScopeLaunchWithoutAWord(t *testing.T) {
	f := &fakeScopes{launch: "silent"}
	s, sock := newTestHolderWith(t, func(c *Config) { c.Scopes = f.scopes(t) })
	cl := dial(t, sock)
	e := startRole(t, cl, "echoer")
	waitScreen(t, cl, e.ID, "hello "+e.ID)
	if got := scopeOf(s, e.ID); got != unitOf("echoer", e.ID) {
		t.Errorf("the terminal's scope %q", got)
	}
	if !strings.Contains(f.logText(), "no word from agent-enter") {
		t.Errorf("log %q", f.logText())
	}
}

// A terminal's scope is stopped when its client exits, and when the holder
// closes it.
func TestScopeStoppedAtTerminalEnd(t *testing.T) {
	f := &fakeScopes{}
	_, sock := newTestHolderWith(t, func(c *Config) { c.Scopes = f.scopes(t) })
	cl := dial(t, sock)
	x := startRole(t, cl, "exiter")
	e := startRole(t, cl, "echoer")
	f.waitStopped(t, unitOf("exiter", x.ID))
	if slices.Contains(f.stopped(), unitOf("echoer", e.ID)) {
		t.Fatal("a live terminal's scope stopped")
	}
	if _, err := cl.Call(Close{Term: e.ID}); err != nil {
		t.Fatal(err)
	}
	f.waitStopped(t, unitOf("echoer", e.ID))
}

// Its own stop (SIGTERM: Shutdown) stops every scope of its build, its
// terminals' and a stale one the list shows, never another build's.
func TestScopesStoppedAtHolderStop(t *testing.T) {
	other := cgroot.ScopeUnit("demo", "old.0123456789", "coder", "t1")
	f := &fakeScopes{active: map[string]bool{"old.0123456789": true}}
	s, sock := newTestHolderWith(t, func(c *Config) { c.Scopes = f.scopes(t) })
	cl := dial(t, sock)
	e := startRole(t, cl, "echoer")
	f.mu.Lock()
	f.listed = []string{unitOf("echoer", e.ID), unitOf("coder", "t9"), other}
	f.mu.Unlock()
	s.Shutdown()
	got := f.stopped()
	for _, u := range []string{unitOf("echoer", e.ID), unitOf("coder", "t9")} {
		if !slices.Contains(got, u) {
			t.Errorf("%s not stopped: %q", u, got)
		}
	}
	if slices.Contains(got, other) {
		t.Errorf("another build's scope stopped: %q", got)
	}
}

// The start sweep (§12.5, r5): this build's stale scopes are stopped,
// another build's only when its holder is not active; when the manager
// cannot say, nothing of that build is stopped and the holder says
// "stale?".
func TestScopeSweepAtStart(t *testing.T) {
	stale := unitOf("coder", "t3")
	dead := cgroot.ScopeUnit("demo", "a.0123456789", "coder", "t1")      // A, killed outright
	drain := cgroot.ScopeUnit("demo", "b.0123456789", "architect", "t2") // B, still draining
	unsure := cgroot.ScopeUnit("demo", "c.0123456789", "coder", "t4")
	f := &fakeScopes{listed: []string{stale, dead, drain, unsure, "agentbus-holder.demo.x.scope"},
		active: map[string]bool{"b.0123456789": true}, unsure: map[string]bool{"c.0123456789": true}}
	sc := f.scopes(t)
	newTestHolderWith(t, func(c *Config) { c.Scopes = sc })
	got := f.stopped()
	slices.Sort(got)
	want := []string{dead, stale}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("stopped %q, want %q", got, want)
	}
	if !strings.Contains(f.logText(), unsure+" stale?") {
		t.Errorf("log %q", f.logText())
	}
}

// A list the manager cannot give stops nothing and is said.
func TestScopeSweepListFails(t *testing.T) {
	f := &fakeScopes{listErr: errors.New("Failed to connect to bus")}
	sc := f.scopes(t)
	newTestHolderWith(t, func(c *Config) { c.Scopes = sc })
	if got := f.stopped(); len(got) != 0 {
		t.Errorf("stopped %q", got)
	}
	if !strings.Contains(f.logText(), "Failed to connect to bus") {
		t.Errorf("log %q", f.logText())
	}
}
