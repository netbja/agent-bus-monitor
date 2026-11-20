package main

import (
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
	if got := health.EvaluateHolderDraining(e); got.Status != health.Warn || !strings.Contains(got.Detail, "v2") {
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
