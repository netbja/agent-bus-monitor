package holder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UnitMemory is the holder unit's memory, read by the holder from its own
// cgroup (agent-resource-limits §3.1): bytes in use, the cap (0: none),
// swap in use, and the kernel's OOM kills in the unit since it started.
type UnitMemory struct {
	Current     uint64 `json:"current"`
	Max         uint64 `json:"max,omitempty"`
	SwapCurrent uint64 `json:"swap_current,omitempty"`
	OOMKills    uint64 `json:"oom_kills,omitempty"`
}

// cgroupRoot is where cgroup v2 is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// ownUnitMemory is unitMemory for this process; nil without a holder unit
// (launchd, the supervised holder) or a readable cgroup.
func ownUnitMemory() *UnitMemory {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil
	}
	return unitMemory(string(b), cgroupRoot)
}

// unitMemory reads the unit cgroup named by self (/proc/self/cgroup's text)
// under root: the unified line's path when it ends in a holder unit
// (agentbus-holder.*.service), or its leaf "holder" below one (cap B, step
// 4). Four small file reads; nil when anything is missing.
func unitMemory(self, root string) *UnitMemory {
	var path string
	for line := range strings.SplitSeq(self, "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			path = p
			break
		}
	}
	if filepath.Base(path) == "holder" {
		path = filepath.Dir(path)
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "agentbus-holder.") || !strings.HasSuffix(base, ".service") {
		return nil
	}
	dir := filepath.Join(root, path)
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b)), err == nil
	}
	cur, ok := read("memory.current")
	if !ok {
		return nil
	}
	var m UnitMemory
	m.Current, _ = strconv.ParseUint(cur, 10, 64)
	if v, ok := read("memory.max"); ok && v != "max" {
		m.Max, _ = strconv.ParseUint(v, 10, 64)
	}
	if v, ok := read("memory.swap.current"); ok {
		m.SwapCurrent, _ = strconv.ParseUint(v, 10, 64)
	}
	if v, ok := read("memory.events"); ok {
		for line := range strings.SplitSeq(v, "\n") {
			if n, ok := strings.CutPrefix(line, "oom_kill "); ok {
				m.OOMKills, _ = strconv.ParseUint(n, 10, 64)
			}
		}
	}
	return &m
}
