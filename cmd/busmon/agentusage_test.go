package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// usageAgent is an agent whose fresh observation of its own process carries
// 3c's measure (agent-resource-limits §5).
func usageAgent(now time.Time, mem, top int64, topPID int, cpu *int) *agentState {
	return &agentState{state: "working", stateAt: now, pid: 7, obsOwnPID: true, obsAt: now.Add(-5 * time.Second),
		obsMem: mem, obsTopMem: top, obsTopPID: topPID, obsCPU: cpu}
}

func intp(n int) *int { return &n }

// The mem and cpu cells: sizes as 4.1G, 512M, 900K; CPU as a percentage of
// one core; a dash for unknown, for an aged observation and for one of
// another process (a replaced agent).
func TestUsageCells(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		a        *agentState
		mem, cpu string
	}{
		{usageAgent(now, 4400<<20, 3<<30, 9, intp(180)), "4.3G", "180%"},
		{usageAgent(now, 512<<20, 0, 0, intp(0)), "512M", "0%"},
		{usageAgent(now, 900<<10, 0, 0, nil), "900K", GlyphDash},
		{usageAgent(now, 0, 0, 0, nil), GlyphDash, GlyphDash},
		{func() *agentState {
			a := usageAgent(now, 4<<30, 0, 0, intp(5))
			a.obsAt = now.Add(-time.Hour)
			return a
		}(), GlyphDash, GlyphDash},
		{func() *agentState { a := usageAgent(now, 4<<30, 0, 0, intp(5)); a.obsOwnPID = false; return a }(), GlyphDash, GlyphDash},
	} {
		if got := Line(memCell(c.a, now)).text(); got != c.mem {
			t.Errorf("mem %q, want %q", got, c.mem)
		}
		if got := Line(cpuCell(c.a, now)).text(); got != c.cpu {
			t.Errorf("cpu %q, want %q", got, c.cpu)
		}
	}
}

// The wide table carries mem and cpu after ctx, header included, on a full
// wide row only: below wideBarRow (the shell's Agents beside For you) both go,
// so the message keeps its room.
func TestAgentRowShowsTheUsage(t *testing.T) {
	now := time.Now()
	a := usageAgent(now, 4400<<20, 3<<30, 9, intp(180))
	if row := agentRow(iconsOff, "coder", a, false, TierWide, 160, now).text(); !strings.Contains(row, "4.3G") || !strings.Contains(row, "180%") {
		t.Fatalf("wide row: %q", row)
	}
	if h := agentHeader(iconsOff, TierWide, 160).text(); !strings.Contains(h, "ctx") || !strings.Contains(h, "mem") || !strings.Contains(h, "cpu") ||
		strings.Index(h, "mem") < strings.Index(h, "ctx") {
		t.Fatalf("header: %q", h)
	}
	narrow := wideBarRow - 1
	if row := agentRow(iconsOff, "coder", a, false, TierWide, narrow, now).text(); strings.Contains(row, "4.3G") {
		t.Fatalf("a %d-cell wide row keeps mem: %q", narrow, row)
	}
	if h := agentHeader(iconsOff, TierWide, narrow).text(); strings.Contains(h, "mem") {
		t.Fatalf("a %d-cell header keeps mem: %q", narrow, h)
	}
	if row := agentRow(iconsOff, "coder", a, false, TierMedium, 100, now).text(); strings.Contains(row, "4.3G") {
		t.Fatalf("medium row: %q", row)
	}
}

// The detail's Resources line: memory, CPU and the largest process by size,
// named only when busmon reads its name itself on this host (the
// observation carries none, spec §5).
func TestAgentDetailShowsTheUsage(t *testing.T) {
	now := time.Now()
	saved := procComm
	t.Cleanup(func() { procComm = saved })
	procComm = func(pid int) (string, bool) { return "pytest", pid == 9 }
	text := func(a *agentState) string {
		_, lines := agentDetail("coder", a, false, nil, nil, now)
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l.text() + "\n")
		}
		return b.String()
	}
	got := text(usageAgent(now, 4400<<20, 3<<30, 9, intp(180)))
	if !strings.Contains(got, "4.3G") || !strings.Contains(got, "cpu 180%") || !strings.Contains(got, "largest process 3.0G (pytest, pid 9)") {
		t.Fatalf("detail:\n%s", got)
	}
	if got := text(usageAgent(now, 4400<<20, 3<<30, 10, nil)); !strings.Contains(got, "largest process 3.0G (pid 10)") || strings.Contains(got, "cpu") {
		t.Fatalf("no local name, cpu unknown:\n%s", got)
	}
	if got := text(usageAgent(now, 0, 0, 0, nil)); strings.Contains(got, "Resources") {
		t.Fatalf("nothing measured:\n%s", got)
	}
}

// The poll copies the observation's measure, and clears it when the
// observation no longer carries it.
func TestPollReadsObservedUsage(t *testing.T) {
	now := time.Now()
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"coder": {State: "idle", TS: 1, Pane: "p2", PID: 7}},
		observed: map[string]bus.Observation{"coder": {At: now.UnixMilli(), PID: 7, State: "idle", MemBytes: 4 << 30, TopBytes: 3 << 30, TopPID: 9, CPUPct: intp(180)}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	poll := func() {
		for i := 0; i < 5; i++ {
			pollOnce(context.Background(), f, st, i)
		}
	}
	poll()
	a := st.agents["coder"]
	if a.obsMem != 4<<30 || a.obsTopMem != 3<<30 || a.obsTopPID != 9 || a.obsCPU == nil || *a.obsCPU != 180 {
		t.Fatalf("coder %+v", a)
	}
	f.observed = map[string]bus.Observation{"coder": {At: now.UnixMilli(), PID: 7, State: "idle"}}
	poll()
	if a := st.agents["coder"]; a.obsMem != 0 || a.obsTopMem != 0 || a.obsTopPID != 0 || a.obsCPU != nil {
		t.Fatalf("after an observation without the measure: %+v", a)
	}
	// An agent no longer observed at all keeps nothing either.
	f.observed = map[string]bus.Observation{"coder": {At: now.UnixMilli(), PID: 7, MemBytes: 1 << 30, CPUPct: intp(5)}}
	poll()
	f.observed = map[string]bus.Observation{}
	poll()
	if a := st.agents["coder"]; a.obsMem != 0 || a.obsCPU != nil {
		t.Fatalf("after the observation went: %+v", a)
	}
}
