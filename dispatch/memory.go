package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// An agent near its memory cap (agent-resource-limits §5, step 3e): each
// observation carries the agent's cap, its holder unit's divided by the
// unit's terminals until cap B (step 4) gives each terminal its own, and
// the escalation target is told once per episode when the agent's tree
// reaches limits.warn_at of it, on the context alert's pattern. The episode
// is the agent's process (the mark holds its pid); a fresh reading below
// the threshold ends it, so the next rise tells again. An unknown use or cap
// neither tells nor ends an episode. Sentinel is not woken: it reads
// holder-memory at its daily review.

// MemoryConfig is the memory notice's settings.
type MemoryConfig struct {
	// WarnAt is limits.warn_at, a percentage, read at each pass; nil or 0:
	// no notice.
	WarnAt func() float64
	// Caps reads each holder build's unit cap and terminal count, by build
	// key, once per observation pass; nil or an error: no cap published.
	Caps func(ctx context.Context) (map[string]HolderCap, error)
}

// HolderCap is one holder build's unit memory cap (0: none) and its
// terminals.
type HolderCap struct {
	Max   uint64
	Terms int
}

// Share is one terminal's part of the cap; 0 without a cap or a terminal.
func (c HolderCap) Share() int64 {
	if c.Max == 0 || c.Terms <= 0 {
		return 0
	}
	return int64(c.Max / uint64(c.Terms))
}

// paneCap is the share of the holder build holding pane ("<key>/<terminal>").
func paneCap(caps map[string]HolderCap, pane string) int64 {
	key, _, ok := strings.Cut(pane, "/")
	if !ok {
		return 0
	}
	return caps[key].Share()
}

// memoryAlerts judges this pass's observations: a lost lease is
// ErrNotHolder, any other failure is logged and retried next pass.
func (l *loop) memoryAlerts(ctx context.Context, obs map[string]Observation) error {
	c := l.cfg.Memory
	if c.WarnAt == nil {
		return nil
	}
	warn := c.WarnAt() / 100
	to := l.cfg.EscalateTo
	if warn <= 0 || !bus.ValidName(to) {
		return nil
	}
	names := make([]string, 0, len(obs))
	for a := range obs {
		names = append(names, a)
	}
	sort.Strings(names)
	var marks map[string]string
	for _, a := range names {
		o := obs[a]
		if o.MemBytes <= 0 || o.MemCap <= 0 || o.PID <= 0 {
			continue // unknown: it neither tells nor ends an episode
		}
		mark := bus.MemoryMark(a)
		if float64(o.MemBytes) < warn*float64(o.MemCap) {
			if marks == nil {
				m, err := l.b.QuotaMarks(ctx)
				if err != nil {
					return err
				}
				marks = m
			}
			if _, open := marks[mark]; open {
				if err := l.b.FencedQuotaDel(ctx, l.token, mark); err != nil {
					return err
				}
			}
			continue
		}
		sent, err := l.b.FencedCmdOnce(ctx, l.token, mark, strconv.Itoa(o.PID), dispatchFrom, to, bus.CmdDirective,
			MemoryNoticeText(a, o, warn), to == a)
		if errors.Is(err, bus.ErrSwapInProgress) {
			continue
		}
		if err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "memory notice", err)
			continue
		}
		if sent {
			l.log(map[string]any{"agent": a, "action": "memory-notice", "mem": o.MemBytes, "cap": o.MemCap})
		}
	}
	return nil
}

// MemoryNoticeText is the notice to the escalation target: the agent, its
// use and its cap, and its largest process's size and pid, never its name
// (spec §5).
func MemoryNoticeText(agent string, o Observation, warn float64) string {
	top := ""
	if o.TopBytes > 0 && o.TopPID > 0 {
		top = fmt.Sprintf(", largest process %s (pid %d)", gib(o.TopBytes), o.TopPID)
	}
	return fmt.Sprintf("memory: %s uses %s of its %s share of the holder's cap (%d %%, limits.warn_at %.0f %%)%s. "+
		"At the cap the kernel kills one process of the holder unit, maybe another agent's. Look at its page in busmon; "+
		"ask it to stop what it runs, or run its tests under agentbus run --cap.",
		agent, gib(o.MemBytes), gib(o.MemCap), o.MemBytes*100/o.MemCap, warn*100, top)
}

// gib is n bytes in GiB, one decimal ("21.0G").
func gib(n int64) string {
	return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
}
