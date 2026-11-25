package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const auditUsage = "usage: audit [--action <a>] [--actor <name>] [--since <d>] [--json] | audit export --since <d> | audit verify"

// runAudit reads the audit trail (audit spec §4): the list, newest first;
// export, JSON Lines oldest first from --since; verify, the hash chain
// (exit 3 broken).
func runAudit(ctx context.Context, b *bus.Bus, args []string, now func() time.Time, out, errw io.Writer) int {
	if len(args) > 0 && args[0] == "verify" {
		if len(args) != 1 {
			fmt.Fprintln(errw, auditUsage)
			return 2
		}
		return auditVerify(ctx, b, out, errw)
	}
	export := len(args) > 0 && args[0] == "export"
	if export {
		args = args[1:]
	}
	args, action := extractFlag(args, "--action")
	args, actor := extractFlag(args, "--actor")
	args, sinceText := extractFlag(args, "--since")
	args, asJSON := extractBool(args, "--json")
	if len(args) != 0 || (export && (action != "" || actor != "" || asJSON)) {
		fmt.Fprintln(errw, auditUsage)
		return 2
	}
	if action != "" && !slices.Contains(bus.AuditActions, action) {
		fmt.Fprintf(errw, "audit: unknown action %q (one of %v)\n", action, bus.AuditActions)
		return 2
	}
	if export && sinceText == "" {
		fmt.Fprintln(errw, "audit export: --since <d> is required (3d, 2026-11-01...)")
		return 2
	}
	var since int64
	if sinceText != "" {
		t, err := parseWhen(sinceText, false, now(), time.Local)
		if err != nil {
			fmt.Fprintf(errw, "audit: --since: %v\n", err)
			return 2
		}
		since = t.UnixMilli()
	}
	entries, err := b.AuditEntries(ctx, since)
	if err != nil {
		fmt.Fprintf(errw, "audit: %v\n", err)
		return 1
	}
	var kept []bus.AuditEntry
	for _, e := range entries {
		if (action == "" || e.Action == action) && (actor == "" || e.Actor == actor) {
			kept = append(kept, e)
		}
	}
	switch {
	case export: // JSON Lines, oldest first, already redacted (written so)
		enc := json.NewEncoder(out)
		for i := len(kept) - 1; i >= 0; i-- {
			if err := enc.Encode(kept[i]); err != nil {
				fmt.Fprintf(errw, "audit export: %v\n", err)
				return 1
			}
		}
	case asJSON:
		if kept == nil {
			kept = []bus.AuditEntry{}
		}
		data, _ := json.MarshalIndent(kept, "", "  ")
		fmt.Fprintln(out, string(data))
	default:
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "TIME\tACTION\tACTOR\tOUTCOME\tTARGET\tREF\tDETAIL")
		for _, e := range kept {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", time.UnixMilli(e.At).Format("2006-01-02 15:04:05"), e.Action, e.Actor,
				e.Outcome, dash(e.Target), dash(e.Ref), dash(e.Detail))
		}
		tw.Flush()
	}
	return 0
}

// auditVerify prints the chain's check and what it cannot see (Q3 B).
func auditVerify(ctx context.Context, b *bus.Bus, out, errw io.Writer) int {
	c, err := b.AuditVerify(ctx)
	if err != nil {
		fmt.Fprintf(errw, "audit verify: %v\n", err)
		return 1
	}
	const limits = "Not seen: a cut of the tail is not detected, and the oldest entries the retention trims cannot be told from a cut of the head. A trail, not a proof."
	if c.Inconclusive { // the head kept moving past the check: neither whole nor broken
		fmt.Fprintf(out, "check inconclusive: %s\n%s\n", c.Reason, limits)
		return 1
	}
	if !c.OK {
		fmt.Fprintf(out, "chain broken at %s: %s (%d entries whole before it)\n%s\n", c.BrokenAt, c.Reason, c.Entries, limits)
		return 3
	}
	if c.Entries == 0 {
		fmt.Fprintf(out, "chain whole: 0 entries\n%s\n", limits)
		return 0
	}
	fmt.Fprintf(out, "chain whole: %d entries, %s to %s\n%s\n", c.Entries, c.First, c.Last, limits)
	return 0
}
