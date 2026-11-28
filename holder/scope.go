package holder

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"

	"github.com/netbja/agent-bus-monitor/cgroot"
	"github.com/netbja/agent-bus-monitor/service"
)

// Scopes is how a holder runs each terminal in its own systemd scope, cap B
// (resource-limits §12.5, step 4b; spec
// docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md). The
// user manager's knowledge stays behind these functions (cgroot,
// cmd/agentbus): the holder names a terminal's scope, starts through it,
// and stops scopes, since a scope is outside the holder's unit and its
// background processes would outlive the holder otherwise.
type Scopes struct {
	// Argv is the scope launch of a client's argv in unit (cgroot.ScopeArgv).
	Argv func(unit string, argv []string) []string
	// List is every agentbus scope of the project, any build.
	List func() ([]string, error)
	// Stop stops units, killing what is left in them.
	Stop func(units ...string) error
	// Active: build key's holder runs; an error, the manager could not say.
	Active func(key string) (bool, error)
	// Log takes the holder's lines about scopes (nil: stderr).
	Log io.Writer
	// EnterWait bounds the wait for agent-enter's word (0: 5 s).
	EnterWait time.Duration
}

const defaultEnterWait = 5 * time.Second

func (s *Server) scopeLog(format string, a ...any) {
	w := s.cfg.Scopes.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "holder: "+format+"\n", a...)
}

// errBeforeExec: systemd-run ended before its exec (agent-enter never ran).
var errBeforeExec = errors.New("systemd-run ended before agent-enter ran")

// startScoped starts c, a scope launch, on a new PTY, and waits for
// agent-enter's word (cgroot.EnterFDEnv). A pipe closed with no word means
// systemd-run ended before its exec: that process is reaped and its PTY
// closed, and errBeforeExec says to start without a scope. No word within
// EnterWait: the launch may still be on its way, so it is kept, and said.
// The caller holds s.mu (the terminal's number is taken).
func (s *Server) startScoped(c *exec.Cmd, id string, ws *pty.Winsize) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	c.ExtraFiles = []*os.File{w} // fd 3
	c.Env = append(c.Env, cgroot.EnterFDEnv+"=3")
	trackMu.Lock()
	f, err := pty.StartWithSize(c, ws)
	if err != nil {
		trackMu.Unlock()
		w.Close()
		return nil, err
	}
	trackedSet[c.Process.Pid] = true
	trackMu.Unlock()
	w.Close()
	wait := s.cfg.Scopes.EnterWait
	if wait <= 0 {
		wait = defaultEnterWait
	}
	_ = r.SetReadDeadline(time.Now().Add(wait))
	b := make([]byte, 1)
	n, rerr := r.Read(b)
	switch {
	case n == 1:
		return f, nil
	case errors.Is(rerr, os.ErrDeadlineExceeded):
		s.scopeLog("terminal %s: no word from agent-enter in %v: its scope launch is kept", id, wait)
		return f, nil
	}
	werr := c.Wait()
	track(c.Process.Pid, false)
	f.Close()
	return nil, fmt.Errorf("%w (%v)", errBeforeExec, werr)
}

// stopScopes stops units (a terminal's end: in its own goroutine, waited
// for by Shutdown).
func (s *Server) stopScopes(units ...string) {
	if err := s.cfg.Scopes.Stop(units...); err != nil {
		s.scopeLog("%v", err)
	}
}

// ownKey is this build's key as a scope name holds it.
func (s *Server) ownKey() string { return cgroot.ScopeKey(service.BuildKey(s.cfg.Build)) }

// sweepScopes is the start sweep (§12.5, r5), and the stop's (own only):
// every agentbus scope of the project, any build. Its own build's is
// stopped when no live terminal holds it; another build's only when that
// build's holder is not active, and when the manager cannot say, it is
// kept and named "stale?".
func (s *Server) sweepScopes(ownOnly bool) {
	units, err := s.cfg.Scopes.List()
	if err != nil {
		s.scopeLog("the scope sweep: %v", err)
		return
	}
	held := map[string]bool{}
	s.mu.Lock()
	for _, t := range s.terms {
		t.mu.Lock()
		if t.alive && t.scope != "" {
			held[t.scope] = true
		}
		t.mu.Unlock()
	}
	s.mu.Unlock()
	own := s.ownKey()
	var stop []string
	for _, u := range units {
		p, key, _, _, ok := cgroot.ParseScopeUnit(u)
		switch {
		case !ok || p != s.cfg.Project || held[u]:
		case key == own:
			stop = append(stop, u)
		case ownOnly:
		default:
			active, err := s.cfg.Scopes.Active(key)
			if err != nil {
				s.scopeLog("scope %s stale? build %s's holder: %v; kept", u, key, err)
				continue
			}
			if !active {
				stop = append(stop, u)
			}
		}
	}
	if len(stop) > 0 {
		s.scopeLog("stopping stale scopes: %v", stop)
		s.stopScopes(stop...)
	}
}
