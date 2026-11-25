package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/update"
)

// The quiet test's broker read (quiet-update-auto §4.3 step 4): the agents
// with their last change and the conductor's missions, at the broker's TIME,
// read without a token.
func TestJudgeProjectReadsAgentsAndMissions(t *testing.T) {
	b, _ := bustest.Open(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "coder", "idle", "done", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireConductor(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	s := mission.Store{B: b, Token: "tok"}
	if err := s.PutChange(ctx, mission.ChangeRecord{Change: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMission(ctx, "c1", mission.MissionRecord{ID: "m1", State: mission.Checking}); err != nil {
		t.Fatal(err)
	}
	d := serviceCmdDeps{Bus: func(string) (*bus.Bus, error) { return b, nil }}
	j, err := judgeProject(ctx, d, b.Project())
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Agents) != 1 || j.Agents[0].Name != "coder" || j.Agents[0].State != "idle" || j.Now.Sub(j.Agents[0].Changed) > time.Minute || j.Now.IsZero() {
		t.Fatalf("agents %+v at %v", j.Agents, j.Now)
	}
	if len(j.Missions) != 1 || j.Missions[0] != (update.MissionFact{Change: "c1", ID: "m1", State: "checking"}) {
		t.Fatalf("missions %+v", j.Missions)
	}
	if why := update.InWork(j); !strings.Contains(why, "agent coder changed its status") {
		t.Errorf("in work: %q", why)
	}
	if _, err := judgeProject(ctx, serviceCmdDeps{Bus: func(string) (*bus.Bus, error) { return nil, errors.New("down") }}, "x"); err == nil {
		t.Error("a broker that cannot be reached judged quiet")
	}
}

// The holders judged: an active one's project, once; an inactive one's not;
// a manager that cannot say fails (the pass is deferred).
func TestActiveHolderProjects(t *testing.T) {
	hm := &fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{}, active: map[string]bool{"k1": true, "k3": true},
		units: []service.HolderUnit{{Project: "a", Key: "k1"}, {Project: "a", Key: "k3"}, {Project: "b", Key: "k2"}}}
	got, err := activeHolderProjects(serviceCmdDeps{Manager: hm})
	if err != nil || len(got) != 1 || got[0] != "a" {
		t.Fatalf("active: %v %v", got, err)
	}
	hm.activeErr = errors.New("systemctl failed")
	if _, err := activeHolderProjects(serviceCmdDeps{Manager: hm}); err == nil {
		t.Error("an unreadable holder state passed")
	}
}
