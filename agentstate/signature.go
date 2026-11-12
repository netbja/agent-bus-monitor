package agentstate

import (
	"regexp"
	"strings"
)

// Signature recognises one client's screen. Ready: any regex matches the input
// line as it looks when empty and submittable. Busy: any regex matches a
// running turn the client's own state may not report. Modal: any regex
// matches a state where Enter would not submit (it vetoes Ready). Ready is
// matched on the ANSI read when ANSI is set (some empty inputs differ from
// typed ones only by their dim placeholder); Busy and Modal always match the
// screen with escapes stripped.
//
// Prompt is the client's input-line marker, matched per line on stripped text.
// Ready is evaluated only on the CURRENT input box: the region from the last
// line matching Prompt to the end of the screen — from the nearest `─` rule
// above it when RuleAbove is set (claude, agy: the box starts with a rule). An
// older box higher in scrollback can therefore neither make a typed current
// box look ready nor veto an empty one. No Prompt line on screen → not ready.
// Busy and Modal still look at the whole screen (the safe direction).
type Signature struct {
	Ready, Busy []*regexp.Regexp
	Modal       []*regexp.Regexp
	// Dialog (A10): the client's own pre-session dialog (trust, hooks review),
	// matched on the stripped screen. It reads blocked even when the host does
	// not say so, and is never ready. Only patterns pinned by a real screen.
	Dialog []*regexp.Regexp
	// Blocked (step 2b, spec 4.3): a menu of the client's waiting on the
	// person (a permission, a question), matched on the bottom of the
	// stripped screen; a named group "what" names what it asks about. Only
	// patterns pinned by a captured screen (BlockedOn).
	Blocked   []*regexp.Regexp
	ANSI      bool
	Prompt    *regexp.Regexp
	RuleAbove bool
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// Strip removes a screen's CSI and OSC escapes.
func Strip(s string) string { return ansiEscape.ReplaceAllString(s, "") }

func anyMatch(rs []*regexp.Regexp, s string) bool {
	for _, r := range rs {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

var ruleLine = regexp.MustCompile(`^[ \t]*─{3,}`)

// inputRegion is the part of screen (raw, escapes kept) that holds the current
// input box: from the last line whose stripped text matches Prompt — or the
// nearest rule above it, with RuleAbove — to the end. ok=false: no input line.
func (s Signature) inputRegion(screen string) (string, bool) {
	if s.Prompt == nil {
		return "", false
	}
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !s.Prompt.MatchString(Strip(lines[i])) {
			continue
		}
		start := i
		if s.RuleAbove {
			for j := i - 1; j >= 0; j-- {
				if ruleLine.MatchString(Strip(lines[j])) {
					start = j
					break
				}
			}
		}
		return strings.Join(lines[start:], "\n"), true
	}
	return "", false
}

// Match judges one screen read: ready to take a submitted line, and busy.
func (s Signature) Match(screen string) (ready, busy bool) {
	screen = strings.ReplaceAll(screen, "\r", "")
	plain := Strip(screen)
	busy = anyMatch(s.Busy, plain)
	ready = s.boxReady(screen) && !anyMatch(s.Modal, plain)
	return ready, busy
}

// boxReady: the current input box (inputRegion) matches Ready, before the
// screen-wide Modal veto. screen has its \r removed.
func (s Signature) boxReady(screen string) bool {
	region, ok := s.inputRegion(screen)
	if !ok {
		return false
	}
	if !s.ANSI {
		region = Strip(region)
	}
	return anyMatch(s.Ready, region)
}

// dialogLines is how far up from the bottom a dialog may start: the words of
// a dialog higher up (accepted, or quoted in a review) are not the dialog.
const dialogLines = 14

// InDialog reports the client's own pre-session dialog at the bottom of the
// screen: a Dialog pattern matched on the last dialogLines non-blank lines of
// the stripped screen.
func (s Signature) InDialog(screen string) bool {
	return anyMatch(s.Dialog, bottomLines(screen))
}
