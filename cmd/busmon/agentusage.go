package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// What an agent's terminal tree uses (agent-resource-limits §5, step 3d):
// the dispatcher's observation carries its memory, its largest process's
// size and pid, and its CPU (step 3c); busmon shows them, from a fresh
// observation of the agent's current process only.

// usageFresh: the observation is the agent's current process's and young
// enough to count.
func usageFresh(a *agentState, now time.Time) bool {
	return a.obsOwnPID && !a.obsAt.IsZero() && now.Sub(a.obsAt) <= observedFresh
}

// sizeText is a size in one short word: 4.3G, 512M, 900K.
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%dM", n>>20)
	}
	return fmt.Sprintf("%dK", n>>10)
}

// memWarnAt is limits.warn_at (a percentage; read once at start): a mem
// cell at it of the agent's cap turns amber, as the dispatcher tells master
// (step 3e).
var memWarnAt = 80.0 // set in main from the environment and config.toml

// memNear: the agent's tree uses memWarnAt of its cap or more; false without
// a cap or with the warning off.
func memNear(a *agentState) bool {
	return memWarnAt > 0 && a.obsCap > 0 && a.obsMem > 0 && float64(a.obsMem) >= memWarnAt/100*float64(a.obsCap)
}

// memCell is the mem column: the tree's resident memory, amber near its
// cap, a dash unknown.
func memCell(a *agentState, now time.Time) []Seg {
	if !usageFresh(a, now) || a.obsMem <= 0 {
		return []Seg{{Text: GlyphDash, Role: RoleMuted}}
	}
	if memNear(a) {
		return []Seg{{Text: sizeText(a.obsMem), Role: RoleAttention}}
	}
	return []Seg{{Text: sizeText(a.obsMem), Role: RoleText}}
}

// cpuCell is the cpu column: the tree's CPU over the dispatcher's window, a
// percentage of one core (180% is 1.8 cores), a dash unknown.
func cpuCell(a *agentState, now time.Time) []Seg {
	if !usageFresh(a, now) || a.obsCPU == nil {
		return []Seg{{Text: GlyphDash, Role: RoleMuted}}
	}
	return []Seg{{Text: strconv.Itoa(*a.obsCPU) + "%", Role: RoleText}}
}

// hostname is this machine's name (os.Hostname; a test's answer).
var hostname = os.Hostname

// procComm is a process's name as this machine's /proc says it; false when
// it cannot be read (a process gone). The observation never carries a name
// (spec §5): it is read only when busmon runs on the agents' host
// (obsLocal), since on another machine the same pid is another program.
var procComm = func(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(b))
	return name, name != ""
}

// usageText is the detail's Resources line, "" when nothing is measured:
// "4.3G, cpu 180%, largest process 3.0G (pytest, pid 9)".
func usageText(a *agentState, now time.Time) string {
	if !usageFresh(a, now) || a.obsMem <= 0 {
		return ""
	}
	parts := []string{sizeText(a.obsMem)}
	if a.obsCap > 0 {
		parts[0] += fmt.Sprintf(" of %s (%d %%)", sizeText(a.obsCap), a.obsMem*100/a.obsCap)
	}
	if a.obsCPU != nil {
		parts = append(parts, "cpu "+strconv.Itoa(*a.obsCPU)+"%")
	}
	if a.obsTopMem > 0 && a.obsTopPID > 0 {
		who := "pid " + strconv.Itoa(a.obsTopPID)
		if name, ok := procComm(a.obsTopPID); ok && a.obsLocal {
			who = Preview(name) + ", " + who
		}
		parts = append(parts, "largest process "+sizeText(a.obsTopMem)+" ("+who+")")
	}
	return strings.Join(parts, ", ")
}
