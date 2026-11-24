package dispatch

import (
	"context"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// Runtime is how the dispatcher sees and touches an agent's terminal: the
// project's holder (HostRuntime, spec 2026-11-10-herdr-exit-design.md 4.2).
type Runtime interface {
	Inspect(ctx context.Context, pane string) Inspection // errors in Inspection.Err
	// Prompt types a wake line (termhost.WakeLine) and reports whether its
	// marker reached the terminal; it takes no text (spec 4.2).
	Prompt(ctx context.Context, pane string, line termhost.Line) (observed bool, err error)
	// Clear types ClearText into the terminal (lifecycle part B): the one
	// other write besides the wake line. It takes no text: the constant is
	// all it can type.
	Clear(ctx context.Context, pane string) error
	// Compact types termhost.CompactLine's /compact (auto-compact): at the
	// urgent fill with no hand-off. It takes no text either.
	Compact(ctx context.Context, pane string) error
}

// ClearText is the only thing Clear types: termhost.ClearLine's text.
var ClearText = termhost.ClearLine().Text()

// Signature is a client's screen signature (agentstate): the judgement and
// the per-client data live there.
type Signature = agentstate.Signature

// Signatures holds only clients probed live, from agentstate's embedded
// client profiles; a client without one is unsupported and never prompted.
// Fixtures: agentstate/testdata/screens.
var Signatures = agentstate.Signatures()

// Supported is the set of clients the dispatcher may prompt.
func Supported() map[string]bool {
	m := make(map[string]bool, len(Signatures))
	for k := range Signatures {
		m[k] = true
	}
	return m
}
