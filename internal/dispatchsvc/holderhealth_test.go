package dispatchsvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

// The evidence merges, per build key, the units (and whether they run), the
// sockets that answer (with their terminals and age) and the binary copies.
func TestHolderEvidence(t *testing.T) {
	root := t.TempDir()
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	cur, old, gone, orphan := service.BuildKey("v3"), service.BuildKey("v2"), service.BuildKey("v1"), service.BuildKey("v0")
	for raw, key := range map[string]string{"v3": cur, "v1": gone, "v0": orphan} { // v2's binary copy is already gone
		dir := service.HolderBinDir(paths, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "build-raw"), []byte(raw+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hm := &fakeHolders{Manager: cmdtest.NewManager(), Active: map[string]bool{cur: true, old: true},
		Units: []service.HolderUnit{{Project: "demo", Key: cur, Raw: "v3"}, {Project: "demo", Key: old, Raw: "v2"},
			{Project: "other", Key: gone, Raw: "v1"}}}
	probe := holderProbe{
		sockets: func(project string) (map[string]time.Duration, error) {
			return map[string]time.Duration{cur: time.Minute, old: 30 * time.Hour}, nil
		},
		answer: func(project, key string) ([]holder.TermInfo, bool) {
			switch key {
			case cur:
				return []holder.TermInfo{{ID: "t1", Alive: true}}, true
			case old:
				return []holder.TermInfo{{ID: "t1", Alive: true}, {ID: "t2"}}, true
			}
			return nil, false
		},
		// The current build answers from a unit cgroup; the old one (no
		// reader answer) reports none (agent-resource-limits §3.1).
		memory: func(project, key string) (*health.HolderMemory, bool) {
			if key == cur {
				return &health.HolderMemory{Current: 5, Max: 10, OOMKills: 1}, true
			}
			return nil, false
		},
	}
	e := holderEvidence(hm, paths, "demo", "v3", probe)
	if !e.Known || len(e.Builds) != 3 { // v3, v2, v0
		t.Fatalf("%+v", e)
	}
	by := map[string]health.HolderBuild{}
	for _, b := range e.Builds {
		by[b.Raw] = b
	}
	if b := by["v3"]; !b.Current || !b.Unit || !b.Active || !b.Answers || b.Terms != 1 || !b.Binary {
		t.Errorf("v3 %+v", b)
	}
	if m := by["v3"].Memory; m == nil || *m != (health.HolderMemory{Current: 5, Max: 10, OOMKills: 1}) {
		t.Errorf("v3 memory %+v", m)
	}
	if m := by["v2"].Memory; m != nil {
		t.Errorf("v2 memory %+v", m)
	}
	if b := by["v2"]; b.Current || !b.Unit || !b.Answers || b.Terms != 2 || b.Since != 30*time.Hour || b.Binary {
		t.Errorf("v2 %+v", b)
	}
	// v1's binary copy is run by another project's unit: never this project's leftover.
	if _, ok := by["v1"]; ok {
		t.Errorf("v1, another project's build, listed: %+v", by["v1"])
	}
	// v0's copy, recorded by no unit at all, is a leftover.
	if b := by["v0"]; b.Unit || b.Answers || !b.Binary {
		t.Errorf("v0 %+v", b)
	}
	if got := health.EvaluateHolderLeftovers(e); got.Status != health.Warn || !strings.Contains(got.Detail, "v0") || strings.Contains(got.Detail, "v1") {
		t.Errorf("leftovers %+v", got)
	}
	if got := health.EvaluateHolderDraining(e, 48*time.Hour, nil, "demo"); got.Status != health.Warn || !strings.Contains(got.Detail, "v2") {
		t.Errorf("draining %+v", got)
	}

	// A manager that cannot say is unknown, never "no holder".
	hm.Err = errors.New("systemctl failed")
	if e := holderEvidence(hm, paths, "demo", "v3", probe); e.Known {
		t.Fatal("an unreadable manager read as known")
	}
}

// holder_restarted's agents from the snapshots: the proved process is
// alive only with the same pid, start time and boot; a reboot since the
// proof means gone.
func TestWithHolderAgentsFromSnapshots(t *testing.T) {
	key := service.BuildKey("v3")
	now := time.UnixMilli(10_000_000)
	at := now.Add(-5 * time.Minute).UnixMilli()
	running := map[int]uint64{7: 70, 8: 80}
	start := func(pid int) (uint64, error) {
		if s, ok := running[pid]; ok {
			return s, nil
		}
		if pid == 9 {
			return 0, errors.New("/proc unreadable")
		}
		return 0, dispatch.ErrNoProcess
	}
	snaps := map[string]bus.AgentSnapshot{
		"coder":     {Pane: key + "/t3", State: "idle", PID: 5, PIDStart: 50, IdentTS: at, BootID: "b1"}, // gone
		"foureyes":  {Pane: key + "/t4", State: "idle", PID: 7, PIDStart: 70, IdentTS: at, BootID: "b1"}, // runs
		"master":    {Pane: key + "/t5", State: "idle", PID: 8, PIDStart: 81, IdentTS: at, BootID: "b1"}, // pid reused
		"docs":      {Pane: key + "/t6", State: "idle", PID: 7, PIDStart: 70, IdentTS: at, BootID: "b0"}, // another boot
		"sentinel":  {Pane: key + "/t7", State: "idle", PID: 7, PIDStart: 70, IdentTS: at},               // boot unknown, runs
		"reviewer":  {Pane: key + "/t9", State: "idle", PID: 9, PIDStart: 90, IdentTS: at, BootID: "b1"}, // lookup failed: unknown, never lost
		"pilot":     {Pane: key + "/t10", State: "idle", PID: 8, IdentTS: at, BootID: "b1"},              // no start time recorded: unproved, never lost
		"architect": {Pane: key + "/t8", State: "done", PID: 6, PIDStart: 60, IdentTS: at, BootID: "b1"}, // gone, but done by itself
	}
	e := withHolderAgents(health.HolderEvidence{Known: true}, snaps, now, provedAlive("b1", start))
	var names []string
	for _, a := range e.Agents {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "coder,docs,master" {
		t.Fatalf("weighed %s", got)
	}
}

type fakeHolders = cmdtest.Holders

// holder-memory keeps the last pass's OOM kill counts: a rise warns at the
// pass that sees it, the next pass is ok again (one notice per episode), and
// limits.warn_at is read at each pass (agent-resource-limits §3.1).
func TestHolderMemoryCheck(t *testing.T) {
	ooms := []uint64{1, 1, 3, 3}
	pct := 80.0
	pass := 0
	now := func() (health.HolderEvidence, error) {
		m := &health.HolderMemory{Current: 50, Max: 100, OOMKills: ooms[pass]}
		return health.HolderEvidence{Known: true, Builds: []health.HolderBuild{{Key: "k", Raw: "v1", Current: true, Answers: true, Memory: m}}}, nil
	}
	run := holderMemoryCheck(now, func() float64 { return pct }, nil)
	for i, want := range []health.Status{health.OK, health.OK, health.Warn, health.OK} {
		pass = i
		out, err := run(context.Background())
		if err != nil || out.Status != want {
			t.Fatalf("pass %d: %+v %v, want %v", i, out, err, want)
		}
	}
	pct = 50
	if out, _ := run(context.Background()); out.Status != health.Warn || !strings.Contains(out.Detail, "50 %") {
		t.Fatalf("warn_at not re-read: %+v", out)
	}
	// Unknown evidence keeps the counts: a rise across it still warns.
	broken := func() (health.HolderEvidence, error) { return health.HolderEvidence{}, errors.New("x") }
	pass = 0
	ok := true
	run = holderMemoryCheck(func() (health.HolderEvidence, error) {
		if !ok {
			return broken()
		}
		return now()
	}, func() float64 { return 80 }, nil)
	run(context.Background())
	ok = false
	if out, err := run(context.Background()); out.Status != health.Unknown || err == nil {
		t.Fatalf("broken pass %+v %v", out, err)
	}
	ok, pass = true, 2
	if out, _ := run(context.Background()); out.Status != health.Warn {
		t.Fatalf("rise across an unknown pass: %+v", out)
	}
	// Known evidence whose memory read failed (Describe failed): the count
	// is kept, so 1, unread, 3 warns (reviewer-codex).
	unread := false
	pass = 0
	run = holderMemoryCheck(func() (health.HolderEvidence, error) {
		e, err := now()
		if unread {
			e.Builds[0].Memory = nil
		}
		return e, err
	}, func() float64 { return 80 }, nil)
	run(context.Background())
	unread = true
	run(context.Background())
	unread, pass = false, 2
	if out, _ := run(context.Background()); out.Status != health.Warn || !strings.Contains(out.Detail, "OOM kill") {
		t.Fatalf("rise across a pass without a memory read: %+v", out)
	}
}

// The agents each holder build holds, from their snapshots' panes, sorted;
// a done agent and a pane of no holder are not counted (§3.2's names).
func TestHeldByBuild(t *testing.T) {
	k1, k2 := service.BuildKey("v1"), service.BuildKey("v2")
	got := heldByBuild(map[string]bus.AgentSnapshot{
		"foureyes": {Pane: k1 + "/t2"}, "coder": {Pane: k1 + "/t1"}, "master": {Pane: k2 + "/t1"},
		"pop": {Pane: k1 + "/t3", State: "done"}, "old": {Pane: "%12"},
	})
	if len(got) != 2 || strings.Join(got[k1], ",") != "coder,foureyes" || strings.Join(got[k2], ",") != "master" {
		t.Fatalf("%v", got)
	}
}

// holder-draining's pass reads the agents and limits.holder_drain_stale_after
// each time; unreadable agents leave the fail's names unknown.
func TestHolderDrainingCheck(t *testing.T) {
	k1, k2 := service.BuildKey("v1"), service.BuildKey("v2")
	now := func() (health.HolderEvidence, error) {
		return health.HolderEvidence{Known: true, Builds: []health.HolderBuild{
			{Key: k2, Raw: "v2", Current: true, Answers: true, Since: 7 * time.Hour},
			{Key: k1, Raw: "v1", Answers: true, Terms: 1, Since: 30 * time.Hour}}}, nil
	}
	snaps := map[string]bus.AgentSnapshot{"coder": {Pane: k1 + "/t1"}}
	var agentsErr error
	agents := func(context.Context) (map[string]bus.AgentSnapshot, error) { return snaps, agentsErr }
	stale := 6 * time.Hour
	run := holderDrainingCheck("demo", agents, now, func() time.Duration { return stale })
	if out, _ := run(context.Background()); out.Status != health.Fail || !strings.Contains(out.Detail, "holds coder: agentbus holder drain demo") {
		t.Fatalf("stale: %+v", out)
	}
	stale = 8 * time.Hour
	if out, _ := run(context.Background()); out.Status != health.Warn {
		t.Fatalf("limit not re-read: %+v", out)
	}
	stale, agentsErr = 6*time.Hour, errors.New("x")
	if out, _ := run(context.Background()); out.Status != health.Fail || !strings.Contains(out.Detail, "agents unknown") {
		t.Fatalf("agents unreadable: %+v", out)
	}
}

// Step 3e: per build, the agent using the most memory, from fresh
// observations of each agent's current process; a done agent, a pane of no
// holder, an aged or another process's observation are not counted.
func TestMostByBuild(t *testing.T) {
	k1, k2 := service.BuildKey("v1"), service.BuildKey("v2")
	const now = int64(1_000_000)
	snaps := map[string]bus.AgentSnapshot{
		"coder": {Pane: k1 + "/t1", PID: 1}, "foureyes": {Pane: k1 + "/t2", PID: 2}, "master": {Pane: k2 + "/t1", PID: 3},
		"pop": {Pane: k1 + "/t3", PID: 4, State: "done"}, "old": {Pane: "%12", PID: 5}, "aged": {Pane: k2 + "/t2", PID: 6},
		"relaunched": {Pane: k2 + "/t3", PID: 70}, "tie": {Pane: k1 + "/t4", PID: 8},
	}
	obs := map[string]bus.Observation{
		"coder": {PID: 1, At: now, MemBytes: 21 << 30}, "foureyes": {PID: 2, At: now, MemBytes: 3 << 30},
		"master": {PID: 3, At: now, MemBytes: 2 << 30}, "pop": {PID: 4, At: now, MemBytes: 40 << 30},
		"old": {PID: 5, At: now, MemBytes: 40 << 30}, "aged": {PID: 6, At: now - bus.ObservedTTL, MemBytes: 40 << 30},
		"relaunched": {PID: 7, At: now, MemBytes: 40 << 30}, "tie": {PID: 8, At: now, MemBytes: 21 << 30},
	}
	got := mostByBuild(snaps, obs, now)
	want := map[string]health.HolderTop{k1: {Agent: "coder", Bytes: 21 << 30}, k2: {Agent: "master", Bytes: 2 << 30}}
	if len(got) != len(want) || got[k1] != want[k1] || got[k2] != want[k2] {
		t.Fatalf("most %+v, want %+v", got, want)
	}
}

// A warn at the cap names the agent most gives for that build.
func TestHolderMemoryCheckNamesTheMost(t *testing.T) {
	now := func() (health.HolderEvidence, error) {
		m := &health.HolderMemory{Current: 90 << 30, Max: 100 << 30}
		return health.HolderEvidence{Known: true, Builds: []health.HolderBuild{{Key: "k", Raw: "v1", Current: true, Answers: true, Memory: m}}}, nil
	}
	most := func(context.Context) map[string]health.HolderTop {
		return map[string]health.HolderTop{"k": {Agent: "coder", Bytes: 21 << 30}, "other": {Agent: "x", Bytes: 1}}
	}
	out, _ := holderMemoryCheck(now, func() float64 { return 80 }, most)(context.Background())
	if out.Status != health.Warn || !strings.HasSuffix(out.Detail, ", most: coder 21.0G") {
		t.Fatalf("%+v", out)
	}
}
