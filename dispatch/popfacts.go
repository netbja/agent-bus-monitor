package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// PopReadOptions are what ReadPopFacts needs besides the bus and the pane.
type PopReadOptions struct {
	Now         time.Time
	RetireAfter time.Duration
	OwnIntent   string // retire --forget's own intent; "" for the dispatch pass and retire's first check
	Episode     int64  // retire's --episode; 0 for the dispatch pass
	// Missions lists the mission attempts in progress on an agent (the
	// conductor's store; injected so dispatch stays free of the mission package).
	Missions func(ctx context.Context, agent string) ([]string, error)
	// ProcStart reads a pid's start time (dispatch.ProcStart); nil never
	// judges a pop closed (pop-closed-by-hand).
	ProcStart func(pid int) (uint64, error)
}

// ProcGone reports whether s's verified process is proved gone: its pid is
// listed no more (ErrNoProcess) or reads another start (a recycled pid). No
// verified identity, no reader or a read that fails otherwise is never gone:
// fail closed (reviewer-codex).
func ProcGone(s *bus.AgentSnapshot, procStart func(int) (uint64, error)) bool {
	if s == nil || s.PID == 0 || procStart == nil {
		return false
	}
	start, err := procStart(s.PID)
	if err != nil {
		return errors.Is(err, ErrNoProcess)
	}
	return start != s.PIDStart
}

// ReadPopFacts reads every fact of §2 for role, the dispatch pass and retire
// --forget alike. A failed read is named in Unknown, never taken as empty.
func ReadPopFacts(ctx context.Context, b *bus.Bus, rt Runtime, role string, o PopReadOptions) PopFacts {
	f := PopFacts{Now: o.Now, RetireAfter: o.RetireAfter, OwnIntent: o.OwnIntent, Episode: o.Episode}
	unknown := func(what string, err error) bool {
		if err != nil {
			f.Unknown = append(f.Unknown, what)
			return true
		}
		return false
	}
	snaps, err := b.Agents(ctx)
	if unknown("agents", err) {
		return f
	}
	if s, ok := snaps[role]; ok {
		f.Snap = &s
	} else {
		return f
	}
	if board, err := b.Board(ctx); !unknown("board", err) {
		for task, e := range board {
			if e.Owner == role && bus.BoardOwes(e.State) {
				f.OwnedTasks = append(f.OwnedTasks, fmt.Sprintf("%s (%s)", task, e.State))
			}
		}
		sort.Strings(f.OwnedTasks)
	}
	if gates, err := b.ReviewsOf(ctx, role); !unknown("gates", err) {
		f.Gates = gates
	}
	if o.Missions == nil {
		f.Unknown = append(f.Unknown, "missions")
	} else if ms, err := o.Missions(ctx, role); !unknown("missions", err) {
		f.Missions = ms
	}
	if asks, err := b.Asks(ctx); !unknown("asks", err) {
		for _, a := range asks {
			if a.From == role && a.Open() > 0 {
				f.OpenAsk = true
			}
		}
	}
	if ob, err := b.Obligation(ctx, role, bus.DefaultHorizon); !unknown("obligation", err) {
		f.Obligation = ob
	}
	if armed, err := b.Armed(ctx, role); !unknown("armed", err) {
		f.Armed = armed
	}
	if w, ok, err := b.Wake(ctx, role); !unknown("wake", err) && ok {
		f.WakeState = w.State
	}
	if in, err := b.SwapIntentRaw(ctx, role); !unknown("swap intent", err) {
		f.Intent = in
	}
	if hs, err := b.Handoffs(ctx); !unknown("hand-off", err) {
		if h, ok := hs[role]; ok {
			void, _ := h.VoidFor(*f.Snap, true)
			f.Handoff = !void
			if f.Handoff {
				f.HandoffReport = h.Report
			}
		}
	}
	if p, err := b.Pause(ctx); !unknown("pause", err) {
		f.Paused = p != nil
	}
	if marks, err := b.QuotaMarks(ctx); !unknown("quota", err) {
		_, f.Held = marks["hold:"+f.Snap.Provider]
		f.Held = f.Held && f.Snap.Provider != ""
	}
	if f.Snap.Pane == "" {
		f.Unknown = append(f.Unknown, "pane")
	} else {
		f.Pane = rt.Inspect(ctx, f.Snap.Pane)
		// Closed: herdr knows no pane or agent there, and the process is gone.
		f.Closed = f.Pane.Gone && ProcGone(f.Snap, o.ProcStart)
	}
	return f
}
