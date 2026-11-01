package bus

import (
	"context"
	"errors"
)

// AutoTurn marks a state flipped by the client's own turn hooks, not declared
// (auto-state spec, docs/superpowers/specs/2026-11-01-auto-state-design.md).
const AutoTurn = "turn"

var (
	// ErrNoFlip: the stored state is not idle or working, or already the
	// state asked for; nothing was written.
	ErrNoFlip = errors.New("no flip: the state is declared, or already that")
	// ErrNotProved: the caller is not the agent's verified process.
	ErrNotProved = errors.New("not the agent's verified process")
)

// TurnState flips an agent between idle and working at its client's turn
// start or end. It writes the {p}:agents snapshot only, never a status entry:
// the state, the Redis TIME, and Auto; the message is kept. Only from idle or
// working, only for the stored verified process (pid and its start), in one
// optimistic transaction, so a declaration landing in between is never
// overwritten. A declaration clears Auto and wins over a flip whatever their
// times (mergeSnapshot).
func (b *Bus) TurnState(ctx context.Context, agent, state string, who AgentIdent) error {
	if !ValidName(agent) || (state != "idle" && state != "working") {
		return ErrNoFlip
	}
	if who.PID <= 0 {
		return ErrNotProved
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		return err
	}
	return b.updateSnapshot(ctx, agent, func(old *AgentSnapshot) (AgentSnapshot, error) {
		switch {
		case old == nil || old.PID != who.PID || old.PIDStart != who.PIDStart:
			return AgentSnapshot{}, ErrNotProved
		case old.State != "idle" && old.State != "working", old.State == state:
			return AgentSnapshot{}, ErrNoFlip
		}
		out := *old
		out.State, out.TS, out.Auto = state, max(now.UnixMilli(), old.TS), AutoTurn
		return out, nil
	})
}
