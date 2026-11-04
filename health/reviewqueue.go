package health

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// review-queue (spec docs/superpowers/specs/2026-11-04-review-queue-design.md):
// the team idles while a review is still owed, a hole team-stall cannot see
// (a task in review is not ready, and an open gate counts as open work). Pure,
// like the stall guard: the reader is stallReader.reviewQueue.

// ReviewQueueWindow: a subject whose deciding approve is older than this is
// history, not a queue (fixed, master's answer).
const ReviewQueueWindow = 72 * time.Hour

// ReviewGate is one open 4-eyes gate on a reviewer.
type ReviewGate struct {
	Reviewer, Ref, Subject string    // Subject "" when the challenge named none
	At                     time.Time // its challenge entry's time; zero when no longer retained (old)
	Delivered              bool      // read by the reviewer: neither unread nor pending
	// DeliveryUnknown: its challenge is no longer retained and the cursor
	// cannot prove it read (reviewer-codex): never owed, named "not judged".
	DeliveryUnknown bool
}

// ReviewVerdict is one ledger entry, in ledger order.
type ReviewVerdict struct {
	Subject, Author, Reviewer, Decision, Head, Ref string
	At                                             time.Time
}

// ReviewQueueInput is everything review-queue judges from.
type ReviewQueueInput struct {
	Now          time.Time
	After        time.Duration // dispatch.stall_after; 0 = off
	ReadErr      string
	Agents       []StallAgent
	NoIdentities bool
	Open         TeamOpenWork // Gates is ignored: an owed gate is what this check reports
	Gates        []ReviewGate
	Verdicts     []ReviewVerdict
	Challenges   map[string][]string // retained challenge ref -> its targets (one ref may challenge several)
	DoneSubjects map[string]bool     // subjects of plan tasks recorded done (their pr:<n> too)
}

// owedReview is one review the queue holds: its key (the wake's episode) and
// its phrase (the detail).
type owedReview struct{ key, text string }

// EvaluateReviewQueue: the team idle for After, nothing else open, and a
// review owed (an open gate delivered and older than After) or half-approved
// (a reviewer who engaged with an approved subject has not approved its
// deciding head) is a warn, in hand by master.
func EvaluateReviewQueue(in ReviewQueueInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (stall_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	}
	idleFor, settled, done := teamIdle(in.Agents, in.Now, in.After, in.NoIdentities)
	if done {
		return settled
	}
	open := in.Open
	open.Gates = 0
	if open.Any() {
		return Outcome{Status: OK, Detail: truncateDetail("work is open: " + strings.Join(open.items(), ", "))}
	}
	owed := owedReviews(in)
	var unjudged []string
	for _, g := range in.Gates {
		if g.DeliveryUnknown {
			unjudged = append(unjudged, fmt.Sprintf("%s's gate %s (challenge no longer retained)", g.Reviewer, g.Ref))
		}
	}
	sort.Strings(unjudged)
	notJudged := ""
	if len(unjudged) > 0 {
		notJudged = "not judged: " + strings.Join(unjudged, ", ")
	}
	if len(owed) == 0 {
		if notJudged != "" {
			return Outcome{Status: Unknown, Detail: truncateDetail(notJudged)}
		}
		return Outcome{Status: OK, Detail: "no review owed"}
	}
	texts := make([]string, len(owed))
	for i, o := range owed {
		texts[i] = o.text
	}
	if notJudged != "" {
		texts = append(texts, notJudged)
	}
	return Outcome{Status: Warn, Detail: truncateDetail(fmt.Sprintf("team idle %s; %s", minutes(idleFor), strings.Join(texts, "; ")))}
}

// ReviewQueueWakes: while review-queue warns, one waking directive to master,
// once per owed set (a new owed review is a new episode).
func ReviewQueueWakes(in ReviewQueueInput, master string) []StallWake {
	out := EvaluateReviewQueue(in)
	if out.Status != Warn {
		return nil
	}
	owed := owedReviews(in)
	keys := make([]string, len(owed))
	for i, o := range owed {
		keys[i] = o.key
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return []StallWake{{Mark: "stall-review", Episode: hex.EncodeToString(sum[:8]), Target: master,
		Text: "Stall guard: the team is idle and a review is owed: " + out.Detail +
			". Re-challenge the reviewer, or record that one approval is enough."}}
}

// owedReviews lists case (a) then case (b), each sorted, so the detail and
// the episode move only when the facts do.
func owedReviews(in ReviewQueueInput) []owedReview {
	dormant := map[string]bool{}
	for _, a := range in.Agents {
		dormant[a.Name] = a.Dormant
	}
	var gates, halves []owedReview
	gatedOn := map[string]bool{} // reviewer|subject with an open gate: case (a)'s, never (b)'s
	for _, g := range in.Gates {
		if g.Subject != "" {
			gatedOn[g.Reviewer+"|"+g.Subject] = true
		}
		age := in.Now.Sub(g.At)
		if !g.Delivered || g.DeliveryUnknown || dormant[g.Reviewer] || (!g.At.IsZero() && age < in.After) {
			continue
		}
		name := g.Subject
		if name == "" {
			name = "gate " + g.Ref
		}
		when := "no longer retained"
		if !g.At.IsZero() {
			when = minutes(age)
		}
		gates = append(gates, owedReview{key: "gate|" + g.Reviewer + "|" + g.Ref,
			text: fmt.Sprintf("%s owes %s (%s)", g.Reviewer, name, when)})
	}
	bySubject := map[string][]ReviewVerdict{}
	var subjects []string
	for _, v := range in.Verdicts {
		if _, seen := bySubject[v.Subject]; !seen {
			subjects = append(subjects, v.Subject)
		}
		bySubject[v.Subject] = append(bySubject[v.Subject], v)
	}
	for _, s := range subjects {
		if in.DoneSubjects[s] {
			continue
		}
		if h, ok := halfApproved(s, bySubject[s], in, gatedOn); ok {
			halves = append(halves, h)
		}
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].key < gates[j].key })
	sort.Slice(halves, func(i, j int) bool { return halves[i].key < halves[j].key })
	return append(gates, halves...)
}

// halfApproved judges one subject (its verdicts in ledger order): approved
// (bus.RollUp's rule) within the window, and a reviewer who engaged with it,
// other than the decider and the author, without an approve of its head.
func halfApproved(subject string, vs []ReviewVerdict, in ReviewQueueInput, gatedOn map[string]bool) (owedReview, bool) {
	decider, lastReject := -1, -1
	for i, v := range vs {
		switch v.Decision {
		case "approve":
			if v.Reviewer != v.Author {
				decider = i
			}
		case "reject":
			lastReject = i
		}
	}
	if decider < 0 || decider < lastReject {
		return owedReview{}, false
	}
	d := vs[decider]
	if in.Now.Sub(d.At) > ReviewQueueWindow {
		return owedReview{}, false
	}
	engaged := map[string]bool{}
	for _, v := range vs {
		engaged[v.Reviewer] = true
		if v.Ref == "" {
			continue
		}
		for _, t := range in.Challenges[v.Ref] {
			engaged[t] = true
		}
	}
	for _, g := range in.Gates {
		if g.Subject == subject {
			engaged[g.Reviewer] = true
		}
	}
	var missing []string
	for r := range engaged {
		if r == d.Reviewer || r == d.Author || gatedOn[r+"|"+subject] {
			continue
		}
		var last *ReviewVerdict
		for i := range vs {
			if vs[i].Reviewer == r {
				last = &vs[i]
			}
		}
		switch {
		case last == nil, last.Decision == "reject":
			missing = append(missing, r)
		case last.Head != "" && d.Head != "" && last.Head != d.Head:
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return owedReview{}, false
	}
	sort.Strings(missing)
	return owedReview{key: "half|" + subject + "|" + d.Head + "|" + strings.Join(missing, ","),
		text: fmt.Sprintf("%s approved by %s, not by %s", subject, d.Reviewer, strings.Join(missing, ", "))}, true
}
