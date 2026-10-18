package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

func runHealth(ctx context.Context, b *bus.Bus, self string, args []string, out, errw io.Writer) int {
	usage := func() int {
		fmt.Fprintln(errw, "usage: health [list | detail <id> | log [<after-stream-id>] | note <id> <text...>]")
		return 2
	}
	if len(args) == 0 || args[0] == "list" {
		records, err := b.Health(ctx)
		if err != nil {
			fmt.Fprintln(errw, "health: could not read health records")
			return 1
		}
		now, err := b.ServerTime(ctx)
		if err != nil {
			fmt.Fprintln(errw, "health: server time unavailable")
			return 1
		}
		for _, d := range health.Definitions() {
			rec, ok := records[d.ID]
			status := health.Unknown
			if ok {
				status = health.EffectiveStatus(rec, now.UnixMilli(), d.Interval)
			}
			fmt.Fprintf(out, "%-20s %-7s %s\n", d.ID, status, d.Title)
		}
		return 0
	}
	switch args[0] {
	case "detail":
		if len(args) != 2 {
			return usage()
		}
		d, ok := health.ByID(args[1])
		if !ok {
			fmt.Fprintln(errw, "health: unknown check")
			return 2
		}
		records, err := b.Health(ctx)
		if err != nil {
			fmt.Fprintln(errw, "health: could not read health records")
			return 1
		}
		notes, err := b.HealthNotes(ctx)
		if err != nil {
			fmt.Fprintln(errw, "health: could not read health notes")
			return 1
		}
		now, err := b.ServerTime(ctx)
		if err != nil {
			fmt.Fprintln(errw, "health: server time unavailable")
			return 1
		}
		rec, exists := records[d.ID]
		status, since, detail := health.Unknown, "-", "no current measurement"
		if exists {
			status = health.EffectiveStatus(rec, now.UnixMilli(), d.Interval)
			if rec.Since > 0 {
				since = time.UnixMilli(rec.Since).Format(time.RFC3339)
			}
			if rec.Detail != "" {
				detail = rec.Detail
			}
		}
		fmt.Fprintf(out, "%s\nstatus: %s\nsince: %s\ndetail: %s\nremediation: %s\n", d.Title, status, since, detail, d.Remediation)
		if note, ok := notes[d.ID]; ok {
			fmt.Fprintf(out, "sentinel note (%s): %s\n", note.Author, note.Text)
		}
		return 0
	case "log":
		if len(args) > 2 {
			return usage()
		}
		after := ""
		if len(args) == 2 {
			after = args[1]
		}
		rows, floor, err := b.HealthTransitions(ctx, after, 100)
		if err != nil {
			fmt.Fprintln(errw, "health: could not read transition log")
			return 1
		}
		if after != "" && floor != "" && after < floor {
			fmt.Fprintf(out, "history before %s has expired\n", floor)
		}
		for _, row := range rows {
			fmt.Fprintf(out, "%s\t%s\t%s\t%s -> %s\n", row.StreamID, time.UnixMilli(row.At).Format(time.RFC3339), row.ID, row.Previous, row.Status)
		}
		return 0
	case "note":
		if len(args) < 3 {
			return usage()
		}
		text := strings.TrimSpace(strings.Join(args[2:], " "))
		if err := b.PutHealthNote(ctx, health.Note{ID: args[1], Author: self, Text: text}); err != nil {
			fmt.Fprintln(errw, "health: note was not saved (invalid or unsafe note, or services are not installed)")
			return 1
		}
		fmt.Fprintln(out, "health note saved")
		return 0
	default:
		return usage()
	}
}
