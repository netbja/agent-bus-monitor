package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// verdictGates closes the gates a verdict by reviewer answers (foryou-gates):
// on the reviewer's own gate, the one named by ref and those opened for
// subject; then, for a ref still open elsewhere, the classic author's gate.
// It returns what to tell the reviewer. A self-verdict closes nothing by any
// path: the author's-gate fallback would otherwise delete the reviewer's own
// gate, the conductor's mission gates included.
func verdictGates(ctx context.Context, b *bus.Bus, author, reviewer, subject, ref string) []string {
	if author == reviewer {
		return []string{"notice: a self-verdict closes no gate (recorded, not counted)"}
	}
	var lines []string
	closed, err := b.CloseReviewGates(ctx, reviewer, author, subject, ref)
	if err != nil {
		lines = append(lines, "notice: closing "+reviewer+"'s gates: "+err.Error())
	}
	if len(closed) > 0 {
		lines = append(lines, fmt.Sprintf("closed gate(s) on %s: %s", reviewer, strings.Join(closed, " ")))
	}
	if ref != "" && !contains(closed, ref) {
		if msg := verdictGateNotice(ctx, b, author, reviewer, ref); msg != "" {
			lines = append(lines, msg)
		}
	}
	return lines
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

type gateRow struct {
	agent, ref, meta string
	g                bus.GateMeta
}

// runGateClose closes stale gates without a verdict: one by agent and ref,
// or a sweep by --subject, --mine (opened by the closer) or --by <name>. An
// agent closes only gates it opened; a person (an explicit --as) closes any. Each close leaves a wake=no reply on the gate's thread, never a
// verdict. --dry-run lists and writes nothing.
func runGateClose(ctx context.Context, b *bus.Bus, self string, args []string, out, errw io.Writer) int {
	args, note := extractFlag(args, "--note")
	args, as := extractFlag(args, "--as")
	args, subject := extractFlag(args, "--subject")
	args, byFlag := extractFlag(args, "--by")
	args, mine := extractBool(args, "--mine")
	args, dry := extractBool(args, "--dry-run")
	usage := func() int {
		fmt.Fprintln(errw, "usage: gate close <agent> <ref> --note <text> [--as <person>]\n"+
			"       gate close (--subject <s> | --mine | --by <name>) [--dry-run] [--note <text>] [--as <person>]")
		return 2
	}
	sweep := subject != "" || mine || byFlag != ""
	if sweep && len(args) != 0 || !sweep && len(args) != 2 {
		return usage()
	}
	if !dry && strings.TrimSpace(note) == "" {
		fmt.Fprintln(errw, "gate close: --note is required (it is the audit trace); --dry-run needs none")
		return 2
	}
	// A person is named explicitly: an inherited AGENT_BUS_HUMAN never widens
	// what an agent may close.
	closer, person := self, false
	if as != "" {
		closer, person = strings.ToLower(as), true
	}
	if !bus.ValidName(closer) {
		fmt.Fprintf(errw, "gate close: invalid closer %q\n", closer)
		return 2
	}

	var rows []gateRow
	if !sweep {
		agent, ref := args[0], args[1]
		open, err := b.OpenChallenges(ctx, agent)
		if err != nil {
			fmt.Fprintf(errw, "gate close: %v\n", err)
			return 1
		}
		meta, ok := open[ref]
		if !ok {
			fmt.Fprintf(errw, "gate close: no open gate %q on %s\n", ref, agent)
			return 1
		}
		rows = append(rows, gateRow{agent, ref, meta, bus.ParseGateMeta(meta)})
	} else {
		all, err := b.AllGates(ctx)
		if err != nil {
			fmt.Fprintf(errw, "gate close: %v\n", err)
			return 1
		}
		by := byFlag
		if mine {
			by = closer
		}
		for agent, h := range all {
			for ref, meta := range h {
				g := bus.ParseGateMeta(meta)
				if subject != "" && g.Subject != subject || by != "" && g.Challenger != by {
					continue
				}
				rows = append(rows, gateRow{agent, ref, meta, g})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].agent != rows[j].agent {
				return rows[i].agent < rows[j].agent
			}
			return rows[i].ref < rows[j].ref
		})
	}

	n, refused := 0, 0
	for _, r := range rows {
		label := r.g.Challenger
		if r.g.Subject != "" {
			label += ", subject " + r.g.Subject
		}
		if r.g.Challenger == bus.ConductorAgent && sweep {
			continue // a mission review is the conductor's: never swept
		}
		// The refusal comes before the dry run, so a preview promises only
		// what the real run would close.
		if !person && r.g.Challenger != closer {
			refused++
			switch {
			case !sweep:
				fmt.Fprintf(errw, "gate close: %s %s was opened by %s; only its challenger or a person (--as) closes it\n", r.agent, r.ref, r.g.Challenger)
			case dry:
				fmt.Fprintf(out, "would skip %s %s: opened by %s\n", r.agent, r.ref, r.g.Challenger)
			default:
				fmt.Fprintf(out, "skipped %s %s: opened by %s\n", r.agent, r.ref, r.g.Challenger)
			}
			continue
		}
		if dry {
			fmt.Fprintf(out, "would close %s %s (opened by %s): %s\n", r.agent, r.ref, label, preview(strings.Join(strings.Fields(r.g.Text), " ")))
			n++
			continue
		}
		ok, err := b.CloseGate(ctx, r.agent, r.ref, r.meta, closer, note, person)
		if err != nil {
			fmt.Fprintf(errw, "gate close: %s %s: %v\n", r.agent, r.ref, err)
			return 1
		}
		if !ok {
			fmt.Fprintf(out, "skipped %s %s: changed or closed meanwhile\n", r.agent, r.ref)
			continue
		}
		if !sweep {
			fmt.Fprintf(out, "closed %s %s (opened by %s)\n", r.agent, r.ref, label)
		}
		n++
	}
	switch {
	case dry:
		fmt.Fprintf(out, "%d gate(s) would close; dry run, nothing written\n", n)
	case sweep:
		fmt.Fprintf(out, "%d gate(s) closed\n", n)
	}
	if refused > 0 {
		return 1
	}
	return 0
}
