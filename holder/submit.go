package holder

import (
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// The submit's error codes (spec 4.2; termhost's errors on the client side).
const (
	CodePersonActive = "person_active" // termhost.ErrPersonActive
	CodeNotReady     = "not_ready"     // termhost.ErrNotReady
	CodeNotLanded    = "not_landed"    // termhost.ErrNotLanded
	CodeLeftBehind   = "left_behind"   // termhost.ErrLeftBehind
)

// Submit's timings (spec 4.2, 4.5).
const (
	DefaultSubmitWait  = 2 * time.Second // dispatch.submit_wait: the line seen in the input
	DefaultPersonQuiet = 3 * time.Second // dispatch.person_quiet: a viewer typed this recently
	settleQuiet        = 80 * time.Millisecond
	settleMax          = 1500 * time.Millisecond
	leftBehindEvery    = 250 * time.Millisecond
)

// clientProfiles maps rolecfg's client names to agentstate's profiles.
var clientProfiles = map[string]string{"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi", "grok": "grok", "agy": "agy"}

// settled is the screen once no output came for settleQuiet, as the
// signatures read it (ANSI lines), with its modes; ok false when the program
// kept writing past settleMax (an unsettled screen proves nothing).
func (t *terminal) settled() (screenText string, bracketed, ok bool) {
	deadline := time.Now().Add(settleMax)
	for {
		t.mu.Lock()
		quiet := time.Since(t.outAt) >= settleQuiet
		if quiet || time.Now().After(deadline) {
			snap := t.scr.Snapshot(0)
			m := t.scr.Modes()
			t.mu.Unlock()
			return snap.ANSI(), m.BracketedPaste, quiet
		}
		t.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
}

// personActive: a draft stands, a viewer typed within quiet, a typing
// viewer is attached and not proven unfocused, or a viewer's lone ESC still
// waits for its sequence (spec 4.5; 5c's escWait: the Escape key or a key
// still arriving, never nobody). A pending ESC past escWait is settled first,
// as the Escape key. Called with t.mu held.
func (t *terminal) personActive(quiet time.Duration) bool {
	now := time.Now()
	for v := range t.viewers {
		if v.observe {
			continue
		}
		t.settle(v, now, false)
		if v.in.state == 'e' {
			return true
		}
	}
	if t.draft || !t.typedAt.IsZero() && time.Since(t.typedAt) < quiet {
		return true
	}
	for v := range t.viewers {
		if !v.observe && v.focused {
			return true
		}
	}
	return false
}

// readyFor: the client's input is empty and submittable on a settled screen
// with bracketed paste on (steps 1 and 3; security S4).
func (t *terminal) readyFor(sig agentstate.Signature) bool {
	screen, bracketed, ok := t.settled()
	return ok && bracketed && sig.EmptyInput(screen)
}

// submit types a closed-set line (spec 4.2), composed here from the frame's
// kind and the holder's own names, under the terminal's input lock, which
// every viewer byte also takes: while it is held, no viewer byte reaches the
// program, and they follow the CR in their order.
//  1. no person active, not left behind, the input empty on a settled
//     screen with mode 2004;
//  2. the hold: the input lock itself;
//  3. the same checks again on a newly settled screen;
//  4. the line as a bracketed paste, and only so;
//  5. wait until the line's token is seen in the current input region;
//  6. seen: CR, and the held bytes follow. Not seen: nothing more is sent,
//     the terminal is marked left behind and the viewers' bytes stay held.
func (s *Server) submit(t *terminal, f Submit) Reply {
	line, token, err := s.lineOf(t, f)
	if err != nil {
		return errReply(CodeBadFrame, "%v", err)
	}
	sig, ok := agentstate.Signatures()[t.profile]
	if !ok || sig.Prompt == nil {
		return errReply(CodeNotReady, "%s's client has no supported signature: never prompted", t.role)
	}
	t.inMu.Lock()
	defer t.inMu.Unlock()
	t.mu.Lock()
	alive, left := t.alive, t.leftBehind
	active := t.personActive(s.cfg.PersonQuiet)
	t.mu.Unlock()
	switch {
	case !alive:
		return errReply(CodeUnknown, "terminal %s has ended", t.id)
	case left:
		return errReply(CodeLeftBehind, "a line may be left in %s's input: no submit until it reads empty", t.role)
	case active:
		return errReply(CodePersonActive, "a person is at %s's terminal", t.role)
	case !t.readyFor(sig):
		return errReply(CodeNotReady, "%s is not ready or its input is not empty", t.role)
	}
	// Step 2 holds: from here, viewer bytes wait on the input lock.
	if s.cfg.afterCheck != nil {
		s.cfg.afterCheck(t)
	}
	if !t.readyFor(sig) { // step 3: a dialog can open between step 1 and the hold
		return errReply(CodeNotReady, "%s changed before the paste; nothing was typed", t.role)
	}
	if _, err := t.pty.Write([]byte("\x1b[200~" + line.Text() + "\x1b[201~")); err != nil {
		return errReply(CodeFailed, "the paste: %v", err)
	}
	for deadline := time.Now().Add(s.cfg.SubmitWait); ; {
		if screen, _, settled := t.settled(); settled && sig.InputHolds(screen, token) {
			_, _ = t.pty.Write([]byte("\r"))
			return Reply{}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.mu.Lock()
	t.leftBehind = true
	t.mu.Unlock()
	t.startHold(f.Kind)
	return errReply(CodeNotLanded, "the line was not seen in %s's input; nothing more sent", t.role)
}

// lineOf composes the line from the frame and the holder's own names (the
// project it serves, the terminal's role): the frame carries no text and no
// name (spec 4.2). token is what the landing check looks for.
func (s *Server) lineOf(t *terminal, f Submit) (termhost.Line, string, error) {
	if f.Kind == "clear" {
		return termhost.ClearLine(), "/clear", nil
	}
	l, err := termhost.WakeLine(s.cfg.Project, t.role, int64(f.Count), f.Nonce)
	return l, "agentbus#" + f.Nonce, err
}

// leftBehindLoop lifts the left-behind mark once a settled screen shows the
// input empty, and then ends the hold, its bytes delivered in order (spec
// 4.2: the only end of the mark; the hold's natural end).
func (t *terminal) leftBehindLoop(sig agentstate.Signature, ok bool) {
	tick := time.NewTicker(leftBehindEvery)
	defer tick.Stop()
	for {
		select {
		case <-t.outDone:
			return
		case <-tick.C:
		}
		t.mu.Lock()
		left := t.leftBehind
		t.mu.Unlock()
		if !left || !ok {
			continue
		}
		if screen, _, settled := t.settled(); settled && sig.EmptyInput(screen) {
			t.mu.Lock()
			t.leftBehind = false
			t.mu.Unlock()
			t.endHold(true)
		}
	}
}
