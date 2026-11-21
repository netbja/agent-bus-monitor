package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The other projects' counter (project-switch-ux item 3, master's yes): the
// header says what waits in this broker's other projects, "other: N for
// you · M merge", and O opens the one with the most for you. busmon reads
// them every othersEvery, a few at a time, with readProjectCounts (reads
// only), and only those whose dispatch runs: a stopped project gets nothing
// new, and is read on M and R as before. A read that fails, or is older
// than othersFresh, drops out of the sum, never counted as 0.

const othersEvery = 2 * time.Minute

// othersFresh is how old a read may be and still count: the period, plus
// the next read's own time, so a project does not drop out while it is read
// again.
var othersFresh = othersEvery + countsReadTimeout

// otherRead is one other project's counts and when they were read.
type otherRead struct {
	counts workCounts
	at     time.Time
}

type othersTickMsg struct{}

type othersMsg struct{ reads map[string]otherRead }

// readOtherCounts reads names but open, countsReadsAtOnce at a time, each
// with its own deadline: those whose dispatch runs (alive), their counts.
// One whose liveness or counts cannot be read is left out.
func readOtherCounts(ctx context.Context, names []string, open string, alive func(context.Context, string) (bool, error),
	counts func(context.Context, string) (workCounts, error), now time.Time) map[string]otherRead {
	out := map[string]otherRead{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, countsReadsAtOnce)
	for _, p := range names {
		if p == open {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			rc, cancel := context.WithTimeout(ctx, countsReadTimeout)
			defer cancel()
			if up, err := alive(rc, p); err != nil || !up {
				return
			}
			n, err := counts(rc, p)
			if err != nil {
				return
			}
			mu.Lock()
			out[p] = otherRead{counts: n, at: now}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// othersSummary is the header's item: the fresh reads but open's, summed;
// "" when nothing waits. Merges are counted apart from the rest of for you.
func othersSummary(reads map[string]otherRead, open string, now time.Time) string {
	you, merge := 0, 0
	for p, r := range reads {
		if p == open || now.Sub(r.at) > othersFresh {
			continue
		}
		you += max(0, r.counts.you-r.counts.merge)
		merge += r.counts.merge
	}
	var parts []string
	if you > 0 {
		parts = append(parts, fmt.Sprintf("%d for you", you))
	}
	if merge > 0 {
		parts = append(parts, fmt.Sprintf("%d merge", merge))
	}
	if len(parts) == 0 {
		return ""
	}
	return "other: " + strings.Join(parts, " · ")
}

// othersCmd reads the other projects in a command; nil without the sources
// (a test shell, no broker).
func (m Model) othersCmd() tea.Cmd {
	src, list, open := m.cfg.admin, m.cfg.listProjects, m.cfg.project
	if src == nil || src.alive == nil || src.counts == nil || list == nil {
		return nil
	}
	ctx := cmp.Or[context.Context](m.cfg.ctx, context.Background())
	return func() tea.Msg {
		lc, cancel := context.WithTimeout(ctx, countsReadTimeout)
		names, err := list(lc)
		cancel()
		if err != nil {
			return othersMsg{}
		}
		return othersMsg{reads: readOtherCounts(ctx, names, open, src.alive, src.counts, time.Now())}
	}
}

func othersTick() tea.Cmd {
	return tea.Tick(othersEvery, func(time.Time) tea.Msg { return othersTickMsg{} })
}

// gotOthers keeps the new reads (an old one ages out by itself) and arms the
// next read.
func (m *Model) gotOthers(msg othersMsg) tea.Cmd {
	if m.others == nil {
		m.others = map[string]otherRead{}
	}
	for p, r := range msg.reads {
		m.others[p] = r
	}
	return othersTick()
}

// openBusiestOther is O: the switch to the other project with the most for
// you as the header counts it (merges apart, reviewer-codex): the most
// non-merge rows, then the most merges, then by name; its read fresh.
func (m *Model) openBusiestOther() tea.Cmd {
	now, best := time.Now(), ""
	var bestYou, bestMerge int
	for p, r := range m.others {
		you, merge := max(0, r.counts.you-r.counts.merge), r.counts.merge
		if p == m.cfg.project || now.Sub(r.at) > othersFresh || you+merge == 0 {
			continue
		}
		if best == "" || you > bestYou || you == bestYou && (merge > bestMerge || merge == bestMerge && p < best) {
			best, bestYou, bestMerge = p, you, merge
		}
	}
	if best == "" {
		m.ui.flash, m.ui.flashErr = "nothing waits in the other projects (read every "+humanAge(othersEvery)+", running ones only)", true
		return nil
	}
	return m.switchProject(best)
}
