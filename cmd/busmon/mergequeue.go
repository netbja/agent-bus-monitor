package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/attention"
	"github.com/netbja/agent-bus-monitor/bus"
)

// The merge queue in busmon (spec docs/superpowers/specs/2026-11-05-merge-queue-design.md §4):
// For you rows for the changes waiting for the person's merge, read from the
// dispatch service's cache on the requests' cadence; busmon never runs git or gh.

// mergeQueueReader is the optional read of the cache (the bus implements it).
type mergeQueueReader interface {
	MergeQueue(ctx context.Context) (bus.MergeQueueRecord, error)
}

// mergeUnknownKey is the one row that says the queue cannot be read: never a
// decision, never opened.
const mergeUnknownKey = attention.MergeUnknownKey

// readMergeQueue reads the cache into st; a failed read keeps the last record
// and says why.
func readMergeQueue(ctx context.Context, b busReader, st *shared) {
	mr, ok := b.(mergeQueueReader)
	if !ok {
		return
	}
	rec, err := mr.MergeQueue(ctx)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.mergeQRead = true
	if err != nil {
		st.mergeQErr = "the merge queue could not be read"
		return
	}
	st.mergeQ, st.mergeQErr = rec, ""
}

// mergeItems are the merge queue's For you rows (attention's: one per item,
// or one row that says why the queue is unknown); nothing without a reader.
func mergeItems(s snapshot) []attentionItem {
	in := attention.Inputs{Dispatch: s.dispatch, MergeRead: s.mergeQRead, Merge: s.mergeQ, MergeErr: s.mergeQErr}
	var out []attentionItem
	for _, it := range attention.Items(in, time.Now()) {
		out = append(out, fromAttention(s, it))
	}
	return out
}

// oneApproval: the item's head has one current approver (attention's).
func oneApproval(it bus.MergeItem) bool { return attention.OneApproval(it) }

// mergePage is an item's page: its fields, y the URL (or, on the local forge,
// the merge command), Y what the reviewers wrote.
func mergePage(rec bus.MergeQueueRecord, head string, width int) (title string, lines []Line, plain, thread string) {
	var it *bus.MergeItem
	for i := range rec.Items {
		if rec.Items[i].Head == head {
			it = &rec.Items[i]
		}
	}
	if it == nil {
		return "Merge", []Line{{S(RoleMuted, "no longer in the merge queue: merged, closed, pushed past or rejected")}}, "", ""
	}
	title = "Merge " + GlyphNone + " " + Preview(it.Branch)
	var f fields
	if it.Number > 0 {
		f.add("PR", S(RoleText, fmt.Sprintf("%d %s %s", it.Number, GlyphNone, Preview(it.Title))))
		if it.URL != "" {
			f.add("Link", S(RoleAccent, Preview(it.URL)))
		}
	}
	f.add("Branch", S(RoleText, Preview(it.Branch)), S(RoleMuted, " at "+it.Head))
	var who []string
	var checks []string
	for _, a := range it.Approvals {
		who = append(who, a.Reviewer+" "+time.UnixMilli(a.At).Local().Format("01-02 15:04"))
		checks = append(checks, a.Reviewer+": "+a.Message)
	}
	f.add("Approved by", S(RoleOK, Preview(strings.Join(who, ", "))))
	if oneApproval(*it) {
		f.add("Approvals", S(RoleAccent, "1 approval"), S(RoleMuted, " (the current approver: "+Preview(it.Approvers[0])+")"))
	}
	f.add("Subject", S(RoleText, Preview(strings.Join(it.Subjects, ", "))))
	src := rec.Source
	if rec.ItemsAt > 0 {
		src += ", read " + time.UnixMilli(rec.ItemsAt).Local().Format("15:04")
	}
	if rec.Reason != "" {
		src += " (the last pass failed: " + rec.Reason + ")"
	}
	f.add("Source", S(RoleMuted, Preview(src)))
	if n := rec.Unapproved; n > 0 {
		others := fmt.Sprintf("%d open PRs not on an approved head", n)
		if n == 1 {
			others = "1 open PR not on an approved head"
		}
		f.add("Others", S(RoleMuted, others+" (a newer push, no review, an approve no longer retained)"))
	}
	var b body
	b.fields(&f, width)
	b.text("\n").seg(RoleMuted, "What the reviewers checked").text("\n")
	for _, c := range checks {
		b.text(DetailText(c)).text("\n")
	}
	plain = it.URL
	if plain == "" && rec.Forge == "local" {
		base := rec.Base
		if base == "" {
			base = "main"
		}
		plain = "git switch " + base + " && git merge --no-ff " + it.Head
	}
	if plain == "" {
		plain = it.Branch
	}
	return title, b.Lines(), plain, strings.Join(checks, "\n")
}
