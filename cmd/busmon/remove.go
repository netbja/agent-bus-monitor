package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// askRemove is d on the selected row: it names what would go and waits for y.
// Agents: their snapshot, usage, wake record and queue (agentbus agents
// forget). Untracked threads: closed by a reply from the person, history kept.
// A tracked request is its owner's to close, so it is only explained.
func (u *ui) askRemove(key string) {
	switch {
	case u.focus == PanelAgents && key != "":
		u.st.mu.Lock()
		a := u.st.agents[key]
		u.st.mu.Unlock()
		if a == nil {
			return
		}
		if why := liveReason(a, time.Now()); why != "" {
			u.flash, u.flashErr = key+" "+why+": not removed", true
			return
		}
		u.confirm = &pendingRemoval{key: "agent:" + key, label: key}
		u.flash, u.flashErr = "forget agent "+key+" (state, usage, queue)? y confirms, any other key cancels", true
	case strings.HasPrefix(key, "thr:"):
		u.st.mu.Lock()
		t, ok := u.findThread(key)
		u.st.mu.Unlock()
		if !ok {
			return
		}
		u.confirm = &pendingRemoval{key: key, label: t.Target}
		u.flash, u.flashErr = fmt.Sprintf("close the %s to %s (history kept)? y confirms, any other key cancels", t.Type, t.Target), true
	case strings.HasPrefix(key, "req:"):
		u.flash, u.flashErr = "a tracked request is closed by its owner (agentbus request done or block)", true
	}
}

// liveReason says why an agent must not be forgotten from busmon, "" if it may.
func liveReason(a *agentState, now time.Time) string {
	switch {
	case a.armed:
		return "is listening on the bus"
	case !a.stateAt.IsZero() && now.Sub(a.stateAt) < staleAfter:
		return "declared a status " + humanAge(now.Sub(a.stateAt))
	}
	return ""
}

func (u *ui) remove(key string) {
	ctx, cancel := context.WithTimeout(u.ctx, pollBudget)
	defer cancel()
	if name, ok := strings.CutPrefix(key, "agent:"); ok {
		if _, err := u.w.ForgetAgent(ctx, name); err != nil {
			u.flash, u.flashErr = "forget failed: "+Preview(err.Error()), true
			return
		}
		u.st.mu.Lock()
		delete(u.st.agents, name)
		u.st.mu.Unlock()
		u.flash, u.flashErr = GlyphDone+" "+name+" forgotten", false
		u.relayout()
		return
	}
	u.st.mu.Lock()
	t, ok := u.findThread(key)
	u.st.mu.Unlock()
	if !ok {
		return
	}
	to := t.From
	if !bus.ValidName(to) {
		to = t.Target
	}
	if _, err := u.w.HumanClose(ctx, u.self, to, t.ID, "closed from busmon by "+u.self); err != nil {
		u.flash, u.flashErr = "close failed: "+Preview(err.Error()), true
		return
	}
	u.st.mu.Lock()
	kept := u.st.threads[:0]
	for _, x := range u.st.threads {
		if x.ID != t.ID {
			kept = append(kept, x)
		}
	}
	u.st.threads = kept
	u.st.mu.Unlock()
	u.flash, u.flashErr = GlyphDone+" thread closed", false
}
