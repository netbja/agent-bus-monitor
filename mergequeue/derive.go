// Package mergequeue derives the merge queue (spec
// docs/superpowers/specs/2026-11-05-merge-queue-design.md): the changes whose
// head is a subject's deciding approved head, still open. Derive is pure;
// the readers (forge.go, git.go) gather its input.
package mergequeue

import (
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
)

// PR is an open change request as the forge lists it.
type PR struct {
	Number                   int
	Title, URL, Branch, Head string
	Base                     string
	Draft                    bool
}

// Tip is a branch and its tip commit (the git fallback: a remote branch, or a
// local one for the local forge).
type Tip struct{ Branch, Head string }

// Input is what Derive joins: the ledger in order, and either the forge's open
// PRs or the branch tips with what is merged.
type Input struct {
	Verdicts []bus.Verdict
	PRs      []PR // the forge's open PRs; nil under the git fallback
	Tips     []Tip
	Merged   func(head string) bool // the git fallback: head is in the base (ancestor, or its contents are)
}

// Result is the queue, and the open PRs not on an approved head (counted, so a
// PR never leaves silently: a newer push, no review, an approve trimmed out,
// a late approve on an older head).
type Result struct {
	Items      []bus.MergeItem
	Unapproved int
}

// approved is a subject's deciding approve.
type approved struct {
	subjects  []string
	approvals []bus.MergeApproval
	at        int64
}

// candidates are the approved deciding heads: per subject, bus.RollUp's
// decider when APPROVED and naming a head (deliver's own rule).
func candidates(vs []bus.Verdict) map[string]*approved {
	bySubject := map[string][]bus.Verdict{}
	var order []string
	for _, v := range vs {
		if _, ok := bySubject[v.Subject]; !ok {
			order = append(order, v.Subject)
		}
		bySubject[v.Subject] = append(bySubject[v.Subject], v)
	}
	sort.Strings(order)
	out := map[string]*approved{}
	for _, s := range order {
		svs := bySubject[s]
		state, idx := bus.RollUp(svs)
		if state != "APPROVED" || svs[idx].Head == "" {
			continue
		}
		h := svs[idx].Head
		a := out[h]
		if a == nil {
			a = &approved{}
			out[h] = a
		}
		a.subjects = append(a.subjects, s)
		a.at = max(a.at, svs[idx].TS)
		for _, v := range svs {
			if v.Decision == "approve" && v.Reviewer != v.Author && v.Head == h {
				a.approvals = append(a.approvals, bus.MergeApproval{Reviewer: v.Reviewer, At: v.TS, Message: v.Message})
			}
		}
	}
	return out
}

// Derive joins the ledger with the open PRs (or the branch tips): an item is
// a change whose head is an approved deciding head. Drafts are never queued
// and never counted; under the git fallback a merged head (Merged) is no item.
func Derive(in Input) Result {
	cand := candidates(in.Verdicts)
	var res Result
	seen := map[string]bool{}
	add := func(it bus.MergeItem, a *approved) {
		if seen[it.Head] {
			return
		}
		seen[it.Head] = true
		it.Subjects, it.Approvals, it.ApprovedAt = a.subjects, a.approvals, a.at
		res.Items = append(res.Items, it)
	}
	for _, pr := range in.PRs {
		if pr.Draft {
			continue
		}
		a := cand[pr.Head]
		if pr.Head == "" || a == nil {
			res.Unapproved++
			continue
		}
		add(bus.MergeItem{Number: pr.Number, Title: pr.Title, URL: pr.URL, Branch: pr.Branch, Head: pr.Head}, a)
	}
	for _, tp := range in.Tips {
		a := cand[tp.Head]
		if a == nil || (in.Merged != nil && in.Merged(tp.Head)) {
			continue
		}
		add(bus.MergeItem{Branch: tp.Branch, Head: tp.Head}, a)
	}
	sort.SliceStable(res.Items, func(i, j int) bool { return res.Items[i].ApprovedAt < res.Items[j].ApprovedAt })
	return res
}
