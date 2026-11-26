package health

import (
	"fmt"
	"strings"
)

// HolderMemory is a holder unit's memory as the holder reads it from its
// own cgroup (agent-resource-limits §3.1): bytes in use, the cap (0 none),
// swap in use, and the unit's OOM kills since it started.
type HolderMemory struct {
	Current, Max, SwapCurrent, OOMKills uint64
}

// EvaluateHolderMemory (holder_memory, §3.1): warn when an answering build's
// unit uses warnAt of its MemoryMax or more, or when its OOM kill count rose
// since prevOOM (the last pass's HolderOOMCounts; a build absent from it
// cannot have risen); unknown when no answering build has a cgroup.
// Per-agent figures come with step 3c, the terminal of a kill with cap B.
func EvaluateHolderMemory(e HolderEvidence, warnAt float64, prevOOM map[string]uint64) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "holder sockets unavailable"}
	}
	var warns, oks, bare []string
	for _, b := range e.Builds {
		if !b.Answers {
			continue
		}
		m := b.Memory
		if m == nil {
			bare = append(bare, b.Raw)
			continue
		}
		if prev, ok := prevOOM[b.Key]; ok && m.OOMKills > prev {
			warns = append(warns, fmt.Sprintf("holder build %s: %d OOM kill(s) since the last pass, terminal unknown (no per-agent caps yet)", b.Raw, m.OOMKills-prev))
		}
		switch {
		case m.Max == 0:
			oks = append(oks, fmt.Sprintf("holder build %s: %s, no cap", b.Raw, gigs(m.Current)))
		case float64(m.Current) >= warnAt*float64(m.Max):
			warns = append(warns, fmt.Sprintf("holder build %s: %s of %s cap (%d %%)", b.Raw, gigs(m.Current), gigs(m.Max), m.Current*100/m.Max))
		default:
			oks = append(oks, fmt.Sprintf("holder build %s: %s of %s cap (%d %%)", b.Raw, gigs(m.Current), gigs(m.Max), m.Current*100/m.Max))
		}
	}
	switch {
	case len(warns) > 0:
		return Outcome{Status: Warn, Detail: strings.Join(warns, "; ")}
	case len(oks) > 0:
		return Outcome{Status: OK, Detail: strings.Join(oks, "; ")}
	case len(bare) > 0:
		return Outcome{Status: Unknown, Detail: "no memory cap on this host: holder build " + strings.Join(bare, ", ") + " runs without a cgroup (launchd or the supervised holder)"}
	}
	return Outcome{Status: Unknown, Detail: "no holder answers"}
}

// HolderOOMCounts is each answering build's OOM kill count, by key: the
// state EvaluateHolderMemory's next pass compares with.
func HolderOOMCounts(e HolderEvidence) map[string]uint64 {
	out := map[string]uint64{}
	for _, b := range e.Builds {
		if b.Answers && b.Memory != nil {
			out[b.Key] = b.Memory.OOMKills
		}
	}
	return out
}

// gigs is n bytes in GiB, one decimal ("35.6G"), as systemd writes a size.
func gigs(n uint64) string {
	return fmt.Sprintf("%.1fG", float64(n)/bytesPerGiB)
}
