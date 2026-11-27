package health

import (
	"strings"
	"testing"
)

// holder_memory (agent-resource-limits §3.1): warn at limits.warn_at of the
// unit's MemoryMax, or when its OOM kill count rose since the last pass;
// unknown without a cgroup (launchd, the supervised holder).
func TestEvaluateHolderMemory(t *testing.T) {
	const gib = 1 << 30
	mem := func(cur, max, ooms uint64) *HolderMemory {
		return &HolderMemory{Current: cur, Max: max, OOMKills: ooms}
	}
	build := func(raw string, current bool, m *HolderMemory) HolderBuild {
		return HolderBuild{Key: raw, Raw: raw, Current: current, Answers: true, Terms: 2, Memory: m}
	}
	withMost := func(b HolderBuild, agent string, n uint64) HolderBuild {
		b.Most = &HolderTop{Agent: agent, Bytes: n}
		return b
	}
	for _, c := range []struct {
		name   string
		e      HolderEvidence
		prev   map[string]uint64
		status Status
		detail []string
	}{
		{"unknown evidence", HolderEvidence{}, nil, Unknown, []string{"unavailable"}},
		{"no holder answers", HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "a", Raw: "a", Current: true}}}, nil, Unknown, []string{"no holder answers"}},
		{"no cgroup", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, nil)}}, nil, Unknown, []string{"no memory cap on this host", "v1"}},
		{"below", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(10*gib, 40*gib, 0))}}, nil, OK, []string{"holder build v1: 10.0G of 40.0G cap (25 %)"}},
		{"at warn_at", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(32*gib, 40*gib, 0))}}, nil, Warn, []string{"holder build v1: 32.0G of 40.0G cap (80 %)"}},
		{"at warn_at, the agent using the most", HolderEvidence{Known: true, Builds: []HolderBuild{withMost(build("v1", true, mem(36*gib, 43*gib, 0)), "coder", 21*gib)}}, nil, Warn,
			[]string{"holder build v1: 36.0G of 43.0G cap (83 %), most: coder 21.0G"}},
		{"below names no agent", HolderEvidence{Known: true, Builds: []HolderBuild{withMost(build("v1", true, mem(10*gib, 40*gib, 0)), "coder", 9*gib)}}, nil, OK, []string{"(25 %)"}},
		{"no cap", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(10*gib, 0, 0))}}, nil, OK, []string{"holder build v1: 10.0G, no cap"}},
		{"first pass knows no rise", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 3))}}, nil, OK, nil},
		{"oom rise", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 3))}}, map[string]uint64{"v1": 1}, Warn,
			[]string{"holder build v1: 2 OOM kill(s) since the last pass", "terminal unknown"}},
		{"oom rise by one", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 3))}}, map[string]uint64{"v1": 2}, Warn,
			[]string{"holder build v1: 1 OOM kill(s) since the last pass"}},
		{"oom unchanged", HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 3))}}, map[string]uint64{"v1": 3}, OK, nil},
		{"draining build counts", HolderEvidence{Known: true, Builds: []HolderBuild{build("v2", true, mem(gib, 40*gib, 0)), build("v1", false, mem(39*gib, 40*gib, 0))}}, nil, Warn,
			[]string{"holder build v1: 39.0G of 40.0G cap (97 %)"}},
		{"a build not answering is not read", HolderEvidence{Known: true, Builds: []HolderBuild{build("v2", true, mem(gib, 40*gib, 0)), {Key: "v1", Raw: "v1", Memory: mem(39*gib, 40*gib, 0)}}}, nil, OK, nil},
	} {
		out := EvaluateHolderMemory(c.e, 0.8, c.prev)
		if out.Status != c.status {
			t.Errorf("%s: status %v (%s), want %v", c.name, out.Status, out.Detail, c.status)
		}
		if c.status == OK && strings.Contains(out.Detail, "most:") {
			t.Errorf("%s: an ok names an agent: %s", c.name, out.Detail)
		}
		for _, d := range c.detail {
			if !strings.Contains(out.Detail, d) {
				t.Errorf("%s: detail %q lacks %q", c.name, out.Detail, d)
			}
		}
	}
	// OOMCounts is the state the next pass compares with.
	e := HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 3)), build("v0", false, nil)}}
	if got := HolderOOMCounts(e, nil); len(got) != 1 || got["v1"] != 3 {
		t.Fatalf("counts %v", got)
	}
	// A pass that cannot read a build's memory keeps its last count
	// (reviewer-codex): 1, unread, 2 still warns. A build no longer listed
	// is dropped.
	prev := HolderOOMCounts(HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 1)), build("v9", false, mem(gib, 40*gib, 5))}}, nil)
	prev = HolderOOMCounts(HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, nil)}}, prev)
	if len(prev) != 1 || prev["v1"] != 1 {
		t.Fatalf("unread pass: %v", prev)
	}
	rise := HolderEvidence{Known: true, Builds: []HolderBuild{build("v1", true, mem(gib, 40*gib, 2))}}
	if out := EvaluateHolderMemory(rise, 0.8, prev); out.Status != Warn || !strings.Contains(out.Detail, "1 OOM kill(s)") {
		t.Fatalf("rise across an unread pass: %+v", out)
	}
	// A build listed but not answering keeps its count too.
	quiet := HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "v1", Raw: "v1", Current: true}}}
	if got := HolderOOMCounts(quiet, map[string]uint64{"v1": 4}); got["v1"] != 4 {
		t.Fatalf("not answering: %v", got)
	}
	if _, ok := ByID("holder-memory"); !ok {
		t.Fatal("holder-memory not defined")
	}
}
