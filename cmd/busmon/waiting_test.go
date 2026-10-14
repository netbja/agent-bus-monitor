package main

import (
	"strings"
	"testing"
	"time"
)

func waitingShared(now time.Time) *shared {
	st := demoShared(now)
	st.agents["master"] = &agentState{state: "waiting", message: "4 decisions for Bernard", stateAt: now.Add(-time.Minute), lastSeen: now.Add(-time.Minute)}
	return st
}

func TestWaitingAgentComesFirstWithThePersonGlyph(t *testing.T) {
	now := time.Now()
	st := waitingShared(now)
	if names := sortedAgentNames(st.agents, ""); names[0] != "master" {
		t.Fatalf("order %v: a waiting agent must come first", names)
	}
	row := agentRow("master", st.agents["master"], false, TierWide, 120, now).text()
	if !strings.Contains(row, GlyphHuman) || !strings.Contains(row, "waiting") {
		t.Fatalf("row %q", row)
	}
	old := &agentState{state: "waiting", stateAt: now.Add(-48 * time.Hour), lastSeen: now.Add(-48 * time.Hour)}
	if dormant(old, false, now) {
		t.Fatal("an agent waiting for a person is never hidden as dormant")
	}
}

func TestPulseFadesOverFourSeconds(t *testing.T) {
	base := time.Unix(1_800_000_000, 0) // a multiple of 4
	var roles []Role
	var bolds []bool
	for i := 0; i < 4; i++ {
		r, b := pulse(base.Add(time.Duration(i) * time.Second))
		roles, bolds = append(roles, r), append(bolds, b)
	}
	if roles[0] != RoleAttention || !bolds[0] || roles[1] != RoleAttention || bolds[1] || roles[2] != RoleMuted || roles[3] != RoleAttention {
		t.Fatalf("roles %v bold %v", roles, bolds)
	}
}

func TestHeaderSaysWhoWaitsForYouAndTheBellRingsOncePerDeclaration(t *testing.T) {
	now := time.Now()
	h := newHarness(t, waitingShared(now))
	if top := strings.SplitN(h.draw(160, 45), "\n", 2)[0]; !strings.Contains(top, GlyphHuman+" 1 waiting for you") {
		t.Fatalf("header %q", top)
	}
	rings := 0
	h.u.bell = func() { rings++ }
	h.u.onTick()
	h.u.onTick()
	if rings != 1 {
		t.Fatalf("rang %d times for one declaration", rings)
	}
	h.u.st.agents["master"].stateAt = now // a new question
	h.u.onTick()
	h.u.st.agents["master"].state = "working"
	h.u.onTick()
	h.u.st.agents["master"].state = "waiting"
	h.u.onTick()
	if rings != 3 {
		t.Fatalf("rings %d, want one per new waiting declaration (3)", rings)
	}
}
