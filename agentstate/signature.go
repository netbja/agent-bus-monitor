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
	// BoxEnd matches the line that closes the input box, below the prompt
	// line (stripped text): the closing rule, the bottom border, or the
	// blank padding line. InputHolds reads the box only, never what a
	// client draws under it (a slash-command menu: foureyes on 5d).
	BoxEnd *regexp.Regexp
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

// EmptyInput reports the current input box empty to its end and
// submittable (spec 4.2 step 1, the architect's ruling of 2026-11-13): Ready
// on the current box, each client's Ready pattern anchored to the box's end
// and pinned by its empty, typed, multiline-typed and returned fixtures,
// with the Modal and Dialog vetoes. A client whose Ready cannot be anchored
// to its box end is unsupported (never prompted). Mode 2004 is the holder's
// to check beside it (screen.Modes).
func (s Signature) EmptyInput(screen string) bool {
	ready, _ := s.Match(screen)
	return ready && !s.InDialog(Strip(strings.ReplaceAll(screen, "\r", "")))
}

// InputHolds reports token in the current input box only: never in an older
// box in scrollback, never under the box (spec 4.2 step 5, the landing
// check; a box with no end on screen holds nothing): the wake
// line's "agentbus#<nonce>", or "/clear". A token wrapped inside the box is
// still read: the region is also compared with its blanks and box borders
// removed. An empty token holds nothing.
func (s Signature) InputHolds(screen, token string) bool {
	if token == "" {
		return false
	}
	region, ok := s.inputBox(strings.ReplaceAll(screen, "\r", ""))
	if !ok {
		return false
	}
	if strings.Contains(region, token) {
		return true
	}
	squeeze := func(x string) string {
		return strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\n', ' ', '│':
				return -1
			}
			return r
		}, x)
	}
	return strings.Contains(squeeze(region), squeeze(token))
}

// inputBox is the current input box, stripped: inputRegion's start through
// the prompt line and the lines after it, up to (not including) the first
// line BoxEnd matches. ok=false: no prompt line, or no end of the box.
func (s Signature) inputBox(screen string) (string, bool) {
	region, ok := s.inputRegion(screen)
	if !ok || s.BoxEnd == nil {
		return "", false
	}
	lines := strings.Split(Strip(region), "\n")
	prompt := -1
	for i, l := range lines {
		if s.Prompt.MatchString(l) {
			prompt = i // the region's last prompt line is its first one after any rule
			break
		}
	}
	if prompt < 0 {
		return "", false
	}
	for i := prompt + 1; i < len(lines); i++ {
		if s.BoxEnd.MatchString(lines[i]) {
			return strings.Join(lines[:i], "\n"), true
		}
	}
	return "", false
}
