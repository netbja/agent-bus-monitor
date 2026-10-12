package main

import (
	"context"
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
)

// verdictGateNotice resolves a verdict's --ref on the author's gate (the
// classic 4-eyes flow) and returns what to tell the reviewer, "" for nothing.
// A mission review is a challenge the conductor opened on the reviewer's own
// gate: the conductor reads the verdict from the ledger and closes that gate
// itself, so it is neither touched here nor reported as missing.
func verdictGateNotice(ctx context.Context, b *bus.Bus, author, reviewer, ref string) string {
	err := b.ResolveChallenge(ctx, author, ref)
	if err == nil {
		return ""
	}
	if open, oerr := b.OpenChallenges(ctx, reviewer); oerr == nil {
		if meta, ok := open[ref]; ok {
			return fmt.Sprintf("review %q is open on %s (%s); the conductor closes it once it reads this verdict", ref, reviewer, preview(meta))
		}
	}
	return "notice: " + err.Error()
}

func preview(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
