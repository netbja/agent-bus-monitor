package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The one send path of the line and of the compose window (v0.16 §7), in
// three parts so that tview (ui.send, all at once) and the step-b shell (the
// calls in a command, busmon Charm spec "Input") apply the same rules:
// planSend decides from the text and the state (the open-question hint, the
// broadcast warning, an unknown recipient, @all's targets), exec makes the
// bus calls, outcome reads their errors into a result and a flash.

// sendInput is what a send decides from.
type sendInput struct {
	tgt, body                string
	directed, window         bool
	known                    bool     // tgt is a registered agent (the window refuses others)
	holders                  []string // the recipients with an open ask
	targets                  []string // @all's targets
	askDraft, broadcastDraft string   // the warnings already given
}

// sendPlan is a send decided: held or refused with its flash, or the calls
// to make (result sendSent until they are made).
type sendPlan struct {
	result                   sendResult
	flash                    string
	flashErr                 bool
	askDraft, broadcastDraft string // the warnings after this send
	tgt, body                string
	directed                 bool
	targets                  []string // one HumanCmd each; none with notify
	notify                   bool     // a broadcast
}

func planSend(in sendInput) sendPlan {
	p := sendPlan{result: sendSent, askDraft: in.askDraft, broadcastDraft: in.broadcastDraft, tgt: in.tgt, directed: in.directed}
	p.body = normalizeCompose(in.body)
	refuse := func(msg string) sendPlan {
		p.result, p.flash, p.flashErr = sendRefused, msg, true
		return p
	}
	if p.body == "" {
		return refuse("nothing to send")
	}
	if in.directed && in.tgt != "all" && (!bus.ValidName(in.tgt) || in.window && !in.known) {
		return refuse("unknown recipient: " + Preview(in.tgt))
	}
	again, answer := "Enter", "Tab"
	if in.window {
		again, answer = "Ctrl+S", "Ctrl+T"
	}
	// v0.15.1 §11.5: a recipient with an open question gets one hint on the
	// first send; sending again sends, Tab (Ctrl+T in the window) answers in
	// its panel.
	if len(in.holders) > 0 && in.askDraft != draftKey(in.tgt, p.body) {
		p.askDraft = draftKey(in.tgt, p.body)
		p.result, p.flash = sendHeld, fmt.Sprintf("%s has an open question. %s: send as a message %s %s: answer in its question panel %s Esc: back to editing",
			strings.Join(in.holders, ", "), again, GlyphNone, answer, GlyphNone)
		return p
	}
	p.askDraft = ""
	if !in.directed && in.broadcastDraft != p.body {
		// Nobody reads a notify and nothing wakes for it: say so once.
		p.broadcastDraft = p.body
		p.result, p.flash, p.flashErr = sendHeld, "nobody reads a broadcast: start with @agent or @all; "+again+" again to broadcast", true
		return p
	}
	p.broadcastDraft = ""
	switch {
	case in.directed && in.tgt == "all":
		if len(in.targets) == 0 {
			return refuse("no agent to send to")
		}
		p.targets = in.targets
	case in.directed:
		p.targets = []string{in.tgt}
	default:
		p.notify = true
	}
	return p
}

// exec makes the plan's calls: every target is tried; the errors are
// index-aligned with the targets (one for a broadcast).
func (p sendPlan) exec(ctx context.Context, w busWriter, self string) []error {
	if p.notify {
		_, err := w.HumanNotify(ctx, self, p.body)
		return []error{err}
	}
	errs := make([]error, len(p.targets))
	for i, a := range p.targets {
		_, errs[i] = w.HumanCmd(ctx, self, a, p.body)
	}
	return errs
}

// outcome reads the calls' errors. Nothing sent keeps the draft (a retry is
// safe); some of @all served clears it (a retry would send them twice).
// pauseNote follows a directive's flash (a pause that could not be read).
func (p sendPlan) outcome(errs []error, pauseNote string) (sendResult, string, bool) {
	var failed []string
	var lastErr error
	for i, err := range errs {
		if err != nil {
			lastErr = err
			if i < len(p.targets) {
				failed = append(failed, p.targets[i])
			}
		}
	}
	switch {
	case lastErr != nil && (p.notify || len(failed) == len(p.targets)):
		return sendFailed, "send failed: " + Preview(lastErr.Error()), true
	case len(failed) > 0:
		return sendPartial, fmt.Sprintf("sent to %d of %d agents; failed: %s (%s)", len(p.targets)-len(failed), len(p.targets),
			strings.Join(failed, ", "), Preview(lastErr.Error())), true
	case p.notify:
		return sendSent, "broadcast sent", false
	case p.tgt == "all":
		return sendSent, fmt.Sprintf("directive sent to %d agents", len(p.targets)) + pauseNote, false
	}
	return sendSent, "directive sent to " + p.tgt + pauseNote, false
}
