package bus

import (
	"context"
	"fmt"
	"sort"
)

// UnknownLosable names §3.1's item 5 when the merge-queue pass's finding for
// a role is absent, stale or unreadable: a terminal-less agent that owes
// nothing else is then lost, "unknown: losable work", never stopped.
const UnknownLosable = "losable work"

// ReadOwes reads §3.1's items for each agent of snaps: the board, the asks,
// the hand-offs and {p}:losable once, the gates and the addressed entries
// per agent. A failed read names its item in Unknown, never an empty owe;
// item 5 is the pass's fresh finding (FreshLosable), else UnknownLosable.
func (b *Bus) ReadOwes(ctx context.Context, snaps map[string]AgentSnapshot) map[string]Owes {
	out := make(map[string]Owes, len(snaps))
	board, boardErr := b.Board(ctx)
	asks, asksErr := b.Asks(ctx)
	hands, handErr := b.Handoffs(ctx)
	losable, losErr := b.LosableRecord(ctx)
	now, nowErr := b.ServerTime(ctx)
	for role, snap := range snaps {
		var o Owes
		if boardErr != nil {
			o.Unknown = append(o.Unknown, "board")
		} else {
			for task, e := range board {
				target := e.Request != nil && e.Request.Target == role
				if (e.Owner == role || target) && BoardOwes(e.State) {
					o.Tasks = append(o.Tasks, fmt.Sprintf("%s (%s)", task, e.State))
				}
			}
			sort.Strings(o.Tasks)
		}
		if gates, err := b.ReviewsOf(ctx, role); err != nil {
			o.Unknown = append(o.Unknown, "gates")
		} else {
			o.Gates = len(gates)
		}
		if asksErr != nil {
			o.Unknown = append(o.Unknown, "asks")
		} else {
			for _, a := range asks {
				o.Ask = o.Ask || a.From == role && a.Open() > 0
			}
		}
		if handErr != nil {
			o.Unknown = append(o.Unknown, "hand-off")
		} else if h, ok := hands[role]; ok {
			void, _ := h.VoidFor(snap, true)
			o.Handoff = !void
		}
		if ob, err := b.Obligation(ctx, role, DefaultHorizon); err != nil {
			o.Unknown = append(o.Unknown, "messages")
		} else {
			o.Messages = int(ob.Size())
		}
		if l, ok := FreshLosable(losable, role, now); ok && losErr == nil && nowErr == nil {
			o.Unpushed = l.Branches
			o.Stash = len(l.Stashes) > 0
		} else {
			o.Unknown = append(o.Unknown, UnknownLosable)
		}
		out[role] = o
	}
	return out
}
