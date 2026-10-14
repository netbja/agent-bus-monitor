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

func TestFadeBreathesBetweenTwentyAndFullOverThreeSeconds(t *testing.T) {
	base := time.UnixMilli(1_800_000_000_000 - 1_800_000_000_000%3000)
	at := func(ms int) int { return fadeLevel(base.Add(time.Duration(ms) * time.Millisecond)) }
	if at(0) != 20 || at(1500) != 100 || at(3000) != 20 {
		t.Fatalf("fade 0/1.5/3s = %d/%d/%d", at(0), at(1500), at(3000))
	}
	if q := at(750); q <= 20 || q >= 100 {
		t.Fatalf("fade at 0.75s = %d, want in between", q)
	}
}

func TestOnlyTheWaitingWordFades(t *testing.T) {
	now := time.Now()
	st := waitingShared(now)
	row := agentRow("master", st.agents["master"], false, TierWide, 120, now)
	faded := 0
	for _, s := range row {
		if s.Fade > 0 {
			faded++
			if !strings.Contains(s.Text, "waiting") {
				t.Fatalf("faded segment %q", s.Text)
			}
		}
	}
	if faded != 1 {
		t.Fatalf("%d faded segments", faded)
	}
	if hl := headerLines(headerData{project: "p", health: health{ok: true}, asks: 1, now: now}, 160, TierWide)[0]; true {
		for _, s := range hl {
			if s.Fade > 0 || (strings.Contains(s.Text, "waiting for you") && s.Bold) {
				t.Fatalf("the header must stay still: %+v", s)
			}
		}
	}
}

func TestFadedColourRunsFromTheLineColourToTheRole(t *testing.T) {
	saved := th
	defer func() { th = saved }()
	th, _ = ResolveTheme("dark", "", "", false, 1<<24)
	if th.Faded(RoleAttention, 100) != th.colors[RoleAttention] || th.Faded(RoleAttention, 0) != th.colors[RoleLine] {
		t.Fatal("fade ends")
	}
	th, _ = ResolveTheme("dark", "", "", false, 16)
	if th.Animated() || th.Faded(RoleAttention, 20) != th.colors[RoleAttention] {
		t.Fatal("16 colours: no fade, full colour")
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
