package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus merge-queue [--json] [--live] (merge-queue spec §5): read-only. It
// prints the dispatch service's cache, or with --live derives the queue now in
// the caller's checkout with the same code and writes nothing. Exit 0 with
// items, 64 with none, 2 when unknown (the reason said, the last items still
// printed).
func runMergeQueue(ctx context.Context, b *bus.Bus, args []string, out io.Writer) int {
	args, asJSON := extractBool(args, "--json")
	args, live := extractBool(args, "--live")
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: merge-queue [--json] [--live]")
		return 2
	}
	var rec bus.MergeQueueRecord
	if live {
		c := serviceMergeQueueChore(b, os.Getenv)
		if c == nil {
			fmt.Fprintln(os.Stderr, "merge-queue: no working directory")
			return 2
		}
		rec = c.derive(ctx)
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
	return printMergeQueue(rec, asJSON, out)
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
	return code
}
