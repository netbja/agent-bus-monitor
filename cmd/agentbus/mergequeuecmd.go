package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// agentbus merge-queue [--json] [--live] (merge-queue spec §5): read-only. It
// prints the dispatch service's cache, or with --live derives the queue now in
// the caller's checkout with the same code and writes nothing. Exit 0 with
// items, 64 with none, 2 when unknown (the reason said, the last items still
// printed).
func runMergeQueue(ctx context.Context, b *bus.Bus, self string, args []string, out io.Writer) int {
	if len(args) > 0 && (args[0] == "drop" || args[0] == "undrop") {
		return runMergeDrop(ctx, b, self, deliverRoleCheck(os.Environ()), args, out, os.Stderr)
	}
	args, asJSON := extractBool(args, "--json")
	args, live := extractBool(args, "--live")
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: merge-queue [--json] [--live] | merge-queue drop <head> \"<reason>\" | merge-queue undrop <head>")
		return 2
	}
	var rec bus.MergeQueueRecord
	if live {
		var ok bool
		if rec, ok = mergeQueueLive(ctx, b, os.Getenv); !ok {
			fmt.Fprintln(os.Stderr, "merge-queue: no working directory")
			return 2
		}
		if now := time.Now().UnixMilli(); rec.Reason == "" {
			rec.AttemptedAt, rec.ItemsAt = now, now
		} else {
			rec.AttemptedAt = now
		}
	} else {
		var err error
		if rec, err = b.MergeQueue(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "merge-queue:", err)
			return 2
		}
	}
	code := printMergeQueue(rec, asJSON, out)
	if !asJSON {
		if ds, err := b.DroppedHeads(ctx); err == nil {
			printDropped(ds, out)
		}
	}
	return code
}

// runMergeDrop: merge-queue drop <head> "<reason>" withdraws an approved
// head nobody will deliver (master-idle §3.1), undrop puts it back. Only who
// may deliver drops (deliver's own check, may); a short SHA is resolved
// against the approved heads (undrop: the dropped ones), an ambiguous or
// unknown one refused. Exit 0 done, 1 refused, 2 a usage or read error.
func runMergeDrop(ctx context.Context, b *bus.Bus, self string, may func(string) error, args []string, out, errw io.Writer) int {
	usage := func() int {
		fmt.Fprintln(errw, "usage: merge-queue drop <head> \"<reason>\" | merge-queue undrop <head>")
		return 2
	}
	drop := args[0] == "drop"
	if (drop && (len(args) != 3 || strings.TrimSpace(args[2]) == "")) || (!drop && len(args) != 2) {
		return usage()
	}
	if drop {
		if r := strings.TrimSpace(args[2]); !health.SafeText(r) || utf8.RuneCountInString(r) > mergeDropReasonMax {
			fmt.Fprintf(errw, "merge-queue drop: the reason must be plain text of at most %d characters, with no credential\n", mergeDropReasonMax)
			return 2
		}
	}
	if err := may(self); err != nil {
		fmt.Fprintf(errw, "merge-queue %s: %v\n", args[0], err)
		return 1
	}
	var known []string
	if drop {
		vs, err := b.Verdicts(ctx, "")
		if err != nil {
			fmt.Fprintln(errw, "merge-queue drop: verdicts unavailable:", err)
			return 2
		}
		for h := range mergequeue.ApprovedHeads(vs) {
			known = append(known, h)
		}
	} else {
		ds, err := b.DroppedHeads(ctx)
		if err != nil {
			fmt.Fprintln(errw, "merge-queue undrop:", err)
			return 2
		}
		for h := range ds {
			known = append(known, h)
		}
	}
	head, why := resolveHead(strings.ToLower(strings.TrimSpace(args[1])), known)
	if why != "" {
		what := "no approved head"
		if !drop {
			what = "not dropped: no dropped head"
		}
		if why == "ambiguous" {
			what = "ambiguous: several heads"
		}
		fmt.Fprintf(errw, "merge-queue %s: %s %q\n", args[0], what, args[1])
		return 2
	}
	if drop {
		if err := b.DropHead(ctx, head, strings.TrimSpace(args[2]), self); err != nil {
			fmt.Fprintln(errw, "merge-queue drop:", err)
			return 2
		}
		fmt.Fprintf(out, "dropped %s: the stall guard no longer counts it as master's to deliver (undrop puts it back)\n", head[:7])
		return 0
	}
	if _, err := b.UndropHead(ctx, head); err != nil {
		fmt.Fprintln(errw, "merge-queue undrop:", err)
		return 2
	}
	fmt.Fprintf(out, "undropped %s\n", head[:7])
	return 0
}

// mergeDropReasonMax bounds a drop's reason (runes).
const mergeDropReasonMax = 200

// resolveHead finds the one head in known that prefix starts (at least 4
// hex characters); why is "unknown" or "ambiguous" otherwise.
func resolveHead(prefix string, known []string) (string, string) {
	if len(prefix) < 4 || len(prefix) > 40 || strings.Trim(prefix, "0123456789abcdef") != "" {
		return "", "unknown"
	}
	found := ""
	for _, h := range known {
		if strings.HasPrefix(h, prefix) {
			if found != "" && found != h {
				return "", "ambiguous"
			}
			found = h
		}
	}
	if found == "" {
		return "", "unknown"
	}
	return found, ""
}

// printDropped lists the dropped heads, oldest first, with their reasons.
func printDropped(ds map[string]bus.MergeDrop, out io.Writer) {
	if len(ds) == 0 {
		return
	}
	heads := make([]string, 0, len(ds))
	for h := range ds {
		heads = append(heads, h)
	}
	sort.Slice(heads, func(i, j int) bool { return ds[heads[i]].At < ds[heads[j]].At })
	parts := make([]string, len(heads))
	for i, h := range heads {
		d := ds[h]
		parts[i] = fmt.Sprintf("%s %s (%s, %s)", h[:7], d.Reason, d.By, time.UnixMilli(d.At).Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(out, "dropped (never to be delivered): %s\n", strings.Join(parts, "; "))
}

// printMergeQueue writes rec and returns the exit code.
func printMergeQueue(rec bus.MergeQueueRecord, asJSON bool, out io.Writer) int {
	code := 0
	switch {
	case rec.Reason != "" || rec.ItemsAt == 0:
		code = 2
	case len(rec.Items) == 0:
		code = 64
	}
	if asJSON {
		if rec.Items == nil {
			rec.Items = []bus.MergeItem{}
		}
		_ = json.NewEncoder(out).Encode(rec)
		return code
	}
	for _, it := range rec.Items {
		id := it.Branch
		if it.Number > 0 {
			id = fmt.Sprintf("#%d", it.Number)
		}
		var who []string
		for _, a := range it.Approvals {
			who = append(who, a.Reviewer)
		}
		line := fmt.Sprintf("%s  %s  %s  approved by %s", id, strings.Join(it.Subjects, ","), it.Head[:min(7, len(it.Head))], strings.Join(who, ", "))
		if it.URL != "" {
			line += "  " + it.URL
		}
		fmt.Fprintln(out, line)
	}
	switch {
	case rec.ItemsAt == 0 && rec.Reason == "":
		fmt.Fprintln(out, "unknown: the merge queue is not computed yet (the dispatch service computes it every 5 minutes)")
	case rec.Reason != "":
		fmt.Fprintln(out, "unknown: "+rec.Reason)
	case len(rec.Items) == 0:
		fmt.Fprintln(out, "nothing waits for a merge")
	}
	if rec.ItemsAt > 0 {
		fmt.Fprintf(out, "source %s, read %s\n", rec.Source, time.UnixMilli(rec.ItemsAt).Local().Format("2006-01-02 15:04"))
	}
	if rec.Unapproved > 0 {
		s := "s"
		if rec.Unapproved == 1 {
			s = ""
		}
		fmt.Fprintf(out, "%d open PR%s not on an approved head (a newer push, no review, an approve no longer retained)\n", rec.Unapproved, s)
	}
	if len(rec.Undelivered) > 0 {
		parts := make([]string, len(rec.Undelivered))
		for i, u := range rec.Undelivered {
			parts[i] = strings.TrimSpace(fmt.Sprintf("%s %s %s", u.Subject, u.Head[:min(7, len(u.Head))], u.Branch))
		}
		fmt.Fprintf(out, "approved, not pushed (master delivers or drops them): %s\n", strings.Join(parts, ", "))
	}
	if len(rec.Conflicting) > 0 {
		prs := make([]string, len(rec.Conflicting))
		for i, c := range rec.Conflicting {
			prs[i] = fmt.Sprintf("#%d onto %s", c.Number, cmp.Or(c.Base, "its base"))
		}
		fmt.Fprintf(out, "approved but conflicting with their base, to rebase: %s\n", strings.Join(prs, ", "))
	}
	return code
}
